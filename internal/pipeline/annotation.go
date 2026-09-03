package pipeline

// annotation.go 注释对齐接线（主规格 §15.6 + 分册 07）。

import (
	"fmt"
	"strings"

	"wenyi/internal/agents"
	"wenyi/internal/ingest"
	"wenyi/internal/punct"
)

// AlignAnnotationsAfterBatch 对范围内全部已完逻辑段串行定位。
func (o *Orchestrator) AlignAnnotationsAfterBatch(ci int, chapter *ingest.Chapter, start, count int, store *RunStore) {
	for _, pos := range completedLogicalStartsInRange(chapter.TextSegments(), start, count) {
		o.AlignSegmentAnnotation(ci, chapter, pos, store)
	}
}

// AlignSegmentAnnotation 串行定位一个已译完逻辑原段的 EPUB 注释链接。
func (o *Orchestrator) AlignSegmentAnnotation(ci int, chapter *ingest.Chapter, startPosition int, store *RunStore) {
	textSegs := chapter.TextSegments()
	if startPosition < 0 || startPosition >= len(textSegs) {
		return
	}
	seg := textSegs[startPosition]
	metadata, _ := seg.Meta["epub_annotations"].(map[string]any)
	if metadata == nil {
		return
	}
	rawItems, _ := metadata["items"].([]any)
	if len(rawItems) == 0 {
		return
	}
	// 回溯 cont 收集逻辑段组
	end := startPosition
	for end+1 < len(textSegs) && textSegs[end+1].Cont {
		end++
	}
	logicalSegments := textSegs[startPosition : end+1]
	for _, s := range logicalSegments {
		if strings.TrimSpace(s.TargetText()) == "" {
			return
		}
	}
	// 先在本逻辑段内做标点定稿
	targetChanged := false
	if o.PunctuationEnabled() {
		targets := make([]string, len(logicalSegments))
		conts := make([]bool, len(logicalSegments))
		for i, s := range logicalSegments {
			targets[i] = s.TargetText()
			conts[i] = s.Cont
		}
		if normalized, err := punct.NormalizeZHSegments(targets, conts); err == nil {
			for i := range normalized {
				if logicalSegments[i].TargetText() != normalized[i] {
					targetChanged = true
				}
				logicalSegments[i].SetTarget(normalized[i])
			}
		}
	}
	var sourceParts, targetParts []string
	for _, s := range logicalSegments {
		sourceParts = append(sourceParts, s.Source)
		targetParts = append(targetParts, s.TargetText())
	}
	source := strings.Join(sourceParts, "")
	target := strings.Join(targetParts, "")
	expectedIDs := map[string]bool{}
	var items []map[string]any
	for _, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := item["id"].(string); ok && id != "" {
			expectedIDs[id] = true
		}
		items = append(items, item)
	}
	placementIDs := map[string]bool{}
	if rawPlacements, ok := metadata["placements"].([]any); ok {
		for _, raw := range rawPlacements {
			if pm, ok := raw.(map[string]any); ok {
				if id, ok := pm["id"].(string); ok {
					placementIDs[id] = true
				}
			}
		}
	}
	digest, _ := metadata["target_digest"].(string)
	idMatch := len(expectedIDs) > 0 && len(placementIDs) == len(expectedIDs)
	for id := range expectedIDs {
		if !placementIDs[id] {
			idMatch = false
			break
		}
	}
	if digest == agents.TargetDigest(target) && idMatch {
		if targetChanged {
			_ = store.SaveChapter(chapter)
		}
		return
	}
	if len(items) == 0 {
		if targetChanged {
			_ = store.SaveChapter(chapter)
		}
		return
	}
	anchor := seg.Anchor
	if anchor == nil {
		anchor = strp(fmt.Sprintf("segment-%d", seg.Index))
	}
	unit := agents.AnnotationUnit{
		UnitID: fmt.Sprintf("ch%d:%s", ci, *anchor),
		Source: source, Target: target, Items: items,
	}
	if !o.Config.Pipeline.AnnotationAlignment {
		_ = store.LogEvent("annotation_alignment_skipped", map[string]any{
			"chapter": ci, "segment": seg.Index, "anchor": *anchor, "unit_id": unit.UnitID,
			"reason": "disabled",
		})
		if targetChanged {
			_ = store.SaveChapter(chapter)
		}
		return
	}
	result := o.AnnotationAligner.AlignUnit(unit)
	metadata["target_digest"] = result.TargetDigest
	placements := make([]any, 0, len(result.Placements))
	for _, p := range result.Placements {
		placements = append(placements, p)
	}
	metadata["placements"] = placements
	_ = store.SaveChapter(chapter)
	_ = store.LogEvent("annotation_alignment_completed", map[string]any{
		"chapter": ci, "segment": seg.Index, "anchor": *anchor, "unit_id": unit.UnitID,
		"annotations": len(items), "used_fallback": result.UsedFallback,
	})
}

func strp(s string) *string { return &s }
