// Package pipeline 编排域（RunStore/Orchestrator/RollingContext）。
package pipeline

import (
	"strings"

	"wenyi/internal/jsonx"
)

// RollingContext 滚动上下文：最近若干段译文尾巴（分册 07 §1）。
type RollingContext struct {
	RecentTargets []string
	MaxRecentKeep int
}

func NewRollingContext(maxRecentKeep int) *RollingContext {
	if maxRecentKeep <= 0 {
		maxRecentKeep = 40
	}
	return &RollingContext{RecentTargets: []string{}, MaxRecentKeep: maxRecentKeep}
}

// Render 取最近 n_recent 段拼接（n<=0 为空）。
func (c *RollingContext) Render(nRecent int) string {
	if nRecent <= 0 || len(c.RecentTargets) == 0 {
		return ""
	}
	start := len(c.RecentTargets) - nRecent
	if start < 0 {
		start = 0
	}
	return strings.Join(c.RecentTargets[start:], "\n")
}

// AddTargets 追加非空译文并截断到容量。
func (c *RollingContext) AddTargets(targets []string) {
	for _, t := range targets {
		if strings.TrimSpace(t) != "" {
			c.RecentTargets = append(c.RecentTargets, t)
		}
	}
	if len(c.RecentTargets) > c.MaxRecentKeep {
		c.RecentTargets = c.RecentTargets[len(c.RecentTargets)-c.MaxRecentKeep:]
	}
}

// ToDict 持久化。
func (c *RollingContext) ToDict() map[string]any {
	return map[string]any{
		"recent_targets":  append([]string{}, c.RecentTargets...),
		"max_recent_keep": c.MaxRecentKeep,
	}
}

// RollingContextFromDict 恢复；min_recent_keep 保证容量随配置扩容。
func RollingContextFromDict(d map[string]any, minRecentKeep int) *RollingContext {
	persisted := 40
	if v, ok := d["max_recent_keep"]; ok {
		switch t := v.(type) {
		case int:
			if t > 0 {
				persisted = t
			}
		case int64:
			if t > 0 {
				persisted = int(t)
			}
		case float64:
			if t > 0 {
				persisted = int(t)
			}
		}
	}
	maxKeep := persisted
	if minRecentKeep > maxKeep {
		maxKeep = minRecentKeep
	}
	var targets []string
	if v, ok := d["recent_targets"]; ok {
		switch arr := v.(type) {
		case []any:
			for _, item := range arr {
				if s, ok := item.(string); ok {
					targets = append(targets, s)
				}
			}
		case []string:
			targets = append(targets, arr...)
		}
	}
	if targets == nil {
		targets = []string{}
	}
	if len(targets) > maxKeep {
		targets = targets[len(targets)-maxKeep:]
	}
	return &RollingContext{RecentTargets: targets, MaxRecentKeep: maxKeep}
}

// 解码 context.json 辅助（jsonx 解析）。
func ParseContextJSON(data []byte) (map[string]any, error) {
	v, err := jsonx.Parse(string(data))
	if err != nil {
		return nil, err
	}
	if m, ok := v.(*jsonx.OMap); ok {
		out := map[string]any{}
		for _, k := range m.Keys() {
			val, _ := m.Get(k)
			out[k] = val
		}
		return out, nil
	}
	return map[string]any{}, nil
}
