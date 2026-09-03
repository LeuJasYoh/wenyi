package llm

// tier 回退链（主规格 §5.3）：缺档沿 fast→cheap→strong 回退，绝不升档。
var tierFallback = map[string][]string{
	"fast":   {"cheap", "strong"},
	"cheap":  {"strong"},
	"strong": {},
}

// Tiered 是带档位解析能力的 client 侧 tier 集合最小接口。
type Tiered[O any] map[string]ResolvedTier[O]

// ResolvedTier 已解析的运行时档位（model + provider 专属 options）。
type ResolvedTier[O any] struct {
	Model   string
	Options O
}

// ResolveTier 命中直接返回；否则按回退链；全缺时返回 strong（缺 strong 由解析期保证不发生）。
func ResolveTier[O any](tiers map[string]ResolvedTier[O], tier string) ResolvedTier[O] {
	if t, ok := tiers[tier]; ok {
		return t
	}
	for _, fb := range tierFallback[tier] {
		if t, ok := tiers[fb]; ok {
			return t
		}
	}
	return tiers["strong"]
}
