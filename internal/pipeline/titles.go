package pipeline

// titles.go 章节标题/目录项翻译（主规格 §15.5 + 分册 07）。

import (
	"errors"
	"fmt"
	"strings"

	"wenyi/internal/agents"
	"wenyi/internal/glossary"
	"wenyi/internal/ingest"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
)

func flat(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TranslateTitles 全书译完才翻标题（书名保持原文）。
func (o *Orchestrator) TranslateTitles(store *RunStore, glossaryStore *glossary.Store, progress ProgressFn) error {
	m, err := store.LoadManifest()
	if err != nil {
		return err
	}
	chapterItems, _ := m["chapters"].([]any)
	flatten := func(s any) string {
		str, _ := s.(string)
		return flat(str)
	}
	meta, _ := m["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	tocEntriesRaw, _ := meta["toc_entries"].([]any)
	type tocEntry struct {
		om map[string]any
	}
	var tocEntries []tocEntry
	for _, raw := range tocEntriesRaw {
		if em, ok := raw.(map[string]any); ok {
			if flatten(em["title"]) != "" {
				tocEntries = append(tocEntries, tocEntry{om: em})
			}
		}
	}
	// anchor → (kind, 完整 source, 完整 target)
	type anchorTarget struct {
		kind        string
		sourceParts string
		targetParts string
	}
	anchorTargets := map[string]anchorTarget{}
	loadedChapters := map[int]*ingest.Chapter{}
	for _, item := range chapterItems {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idx, ok := cm["index"].(int64)
		if !ok {
			continue
		}
		ch, err := store.LoadChapter(int(idx))
		if err != nil {
			return err
		}
		loadedChapters[int(idx)] = ch
	}
	{
		var activeAnchor *string
		activeKind := ""
		complete := false
		var sourceParts, parts []string
		reset := func() {
			activeAnchor = nil
			activeKind = ""
			complete = false
			sourceParts = nil
			parts = nil
		}
		flush := func() {
			if activeAnchor != nil && activeKind == "heading" && complete && len(parts) > 0 {
				anchorTargets[*activeAnchor] = anchorTarget{
					kind:        activeKind,
					sourceParts: strings.Join(sourceParts, ""),
					targetParts: strings.Join(parts, ""),
				}
			}
		}
		for _, ci := range sortedKeys(loadedChapters) {
			for _, seg := range loadedChapters[ci].Segments {
				if seg.Anchor != nil {
					flush()
					activeAnchor = seg.Anchor
					activeKind = seg.Kind
					sourceParts = []string{seg.Source}
					if strings.TrimSpace(seg.TargetText()) != "" {
						parts = []string{seg.TargetText()}
						complete = true
					} else {
						parts = nil
						complete = false
					}
					continue
				}
				if seg.Cont && activeAnchor != nil {
					sourceParts = append(sourceParts, seg.Source)
					if strings.TrimSpace(seg.TargetText()) != "" {
						parts = append(parts, seg.TargetText())
					} else {
						complete = false
					}
					continue
				}
				flush()
				reset()
			}
			flush()
			reset()
		}
	}
	// 目录复用正文译文
	changed := false
	for _, entry := range tocEntries {
		if _, done := entry.om["title_translated"]; done {
			continue
		}
		anchor, _ := entry.om["segment_anchor"].(string)
		target := ""
		if anchor != "" {
			if linked, ok := anchorTargets[anchor]; ok && flat(linked.sourceParts) == flatten(entry.om["title"]) {
				target = linked.targetParts
			}
		}
		if target != "" {
			entry.om["title_translated"] = strings.TrimSpace(target)
			changed = true
		}
	}
	entryByID := map[string]map[string]any{}
	for _, entry := range tocEntries {
		if id, ok := entry.om["entry_id"].(string); ok {
			entryByID[id] = entry.om
		}
	}
	syncChapterTitles := func() {
		for _, item := range chapterItems {
			cm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if _, done := cm["title_translated"]; done {
				continue
			}
			tocID, _ := cm["toc_entry_id"].(string)
			if tocID == "" {
				continue
			}
			if entry, ok := entryByID[tocID]; ok {
				if translated, ok := entry["title_translated"].(string); ok && translated != "" {
					cm["title_translated"] = translated
					changed = true
				}
			}
		}
	}
	syncChapterTitles()
	// spine 回退章
	for _, item := range chapterItems {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, done := cm["title_translated"]; done {
			continue
		}
		if tocID, _ := cm["toc_entry_id"].(string); tocID != "" {
			continue
		}
		idx, ok := cm["index"].(int64)
		if !ok {
			continue
		}
		ch := loadedChapters[int(idx)]
		if ch == nil {
			continue
		}
		for _, seg := range ch.Segments {
			if seg.Kind != "heading" || seg.Anchor == nil {
				continue
			}
			if flat(seg.Source) == flatten(cm["title"]) {
				if linked, ok := anchorTargets[*seg.Anchor]; ok && linked.targetParts != "" {
					cm["title_translated"] = linked.targetParts
					changed = true
				}
			}
			break
		}
	}
	type pendingItem struct {
		record map[string]any
		source string
	}
	var pending []pendingItem
	for _, entry := range tocEntries {
		if _, done := entry.om["title_translated"]; done {
			continue
		}
		pending = append(pending, pendingItem{record: entry.om, source: flatten(entry.om["title"])})
	}
	for _, item := range chapterItems {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, done := cm["title_translated"]; done {
			continue
		}
		if tocID, _ := cm["toc_entry_id"].(string); tocID != "" {
			continue
		}
		title := flatten(cm["title"])
		if title != "" {
			pending = append(pending, pendingItem{record: cm, source: title})
		}
	}
	if changed {
		if err := store.SaveManifest(m); err != nil {
			return err
		}
	}
	if len(pending) == 0 {
		_ = store.LogEvent("titles_skipped", map[string]any{"reason": "already_translated_or_reused"})
		return nil
	}
	// 分批（每批 ≤40 项且累计 ≤4000 字符）
	var batches [][]pendingItem
	var cur []pendingItem
	curLen := 0
	for _, item := range pending {
		l := len([]rune(item.source))
		if len(cur) > 0 && (len(cur) >= 40 || curLen+l > 4000) {
			batches = append(batches, cur)
			cur = nil
			curLen = 0
		}
		cur = append(cur, item)
		curLen += l
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	terms, err := glossaryStore.AllTerms()
	if err != nil {
		return err
	}
	lites := make([]agents.GlossaryTermLite, len(terms))
	for i, t := range terms {
		lites[i] = agents.GlossaryTermLite{
			Source: t.Source, Target: t.Target, Reading: t.Reading,
			Type: t.Type, Gender: t.Gender, Aliases: t.Aliases,
		}
	}
	glossaryText := agents.RenderGlossary(lites)
	for bi, batch := range batches {
		callProgress(progress, 0, 0, "翻译章节标题…")
		titles := make([]string, len(batch))
		for i, item := range batch {
			titles[i] = item.source
		}
		n := len(batch)
		system := agents.Render("title_translator_system", agents.RenderOptions{
			Src: o.Config.SourceLang, Tgt: o.Config.TargetLang,
			Vars: map[string]string{"n": fmt.Sprintf("%d", n)},
		})
		user := agents.Render("title_translator_user", agents.RenderOptions{
			Src: o.Config.SourceLang, Tgt: o.Config.TargetLang,
			Vars: map[string]string{
				"glossary":        glossaryText,
				"n":               fmt.Sprintf("%d", n),
				"numbered_titles": agents.Numbered(titles),
			},
		})
		data, err := o.Client.CompleteJSON([]llm.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		}, "strong", nil, "title_translate")
		if err != nil {
			_ = store.LogEvent("titles_translation_failed", map[string]any{
				"batch": bi, "count": n, "error": fmt.Sprintf("%v", err),
			})
			return err
		}
		var outItems []any
		if om, ok := data.(*jsonx.OMap); ok {
			if v, ok := om.Get("titles"); ok {
				outItems, _ = v.([]any)
			}
		} else if arr, ok := data.([]any); ok {
			outItems = arr
		}
		if outItems == nil || len(outItems) != n {
			actual := 0
			if outItems != nil {
				actual = len(outItems)
			}
			_ = store.LogEvent("titles_translation_rejected", map[string]any{
				"batch": bi, "reason": "count_mismatch", "expected": n, "actual": actual,
			})
			return fmt.Errorf("标题翻译数量不符：期望 %d，实际 %d", n, actual)
		}
		translated := make([]string, n)
		for i, item := range outItems {
			if s, ok := item.(string); ok {
				translated[i] = strings.TrimSpace(s)
			} else {
				translated[i] = ""
			}
		}
		titlesEvent := make([]map[string]any, 0, n)
		for i, item := range batch {
			target := translated[i]
			if target == "" {
				target = item.source
			}
			item.record["title_translated"] = target
			titlesEvent = append(titlesEvent, map[string]any{"source": item.source, "target": target})
		}
		syncChapterTitles()
		if err := store.SaveManifest(m); err != nil {
			return err
		}
		_ = store.LogEvent("titles_translated", map[string]any{"batch": bi, "titles": titlesEvent})
	}
	return nil
}

func sortedKeys[T any](m map[int]T) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

var _ = errors.New
