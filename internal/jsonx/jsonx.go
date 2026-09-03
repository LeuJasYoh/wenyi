// Package jsonx 提供"保留对象键插入顺序"的 JSON 解析与序列化。
// Python 侧审校协议多处依赖 dict 键序（如 list(data)[-2:] == ["reviewed_segments","complete"]），
// Go 标准库 Unmarshal 到 map 会丢键序，故全部 LLM JSON 解析统一走本包。
// 序列化规则对齐 Python json.dumps(..., ensure_ascii=False)：不转义 HTML 字符。
package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// OMap 是保留键插入顺序的 JSON 对象。
type OMap struct {
	keys []string
	m    map[string]any
}

func NewOMap() *OMap { return &OMap{m: map[string]any{}} }

func (o *OMap) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

func (o *OMap) Keys() []string {
	if o == nil {
		return nil
	}
	return append([]string(nil), o.keys...)
}

func (o *OMap) Get(k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.m[k]
	return v, ok
}

// Set 设置键值；新键追加到末尾（保持插入序）。
func (o *OMap) Set(k string, v any) {
	if o.m == nil {
		o.m = map[string]any{}
	}
	if _, exists := o.m[k]; !exists {
		o.keys = append(o.keys, k)
	}
	o.m[k] = v
}

func (o *OMap) Delete(k string) {
	if _, ok := o.m[k]; !ok {
		return
	}
	delete(o.m, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// LastKeys 返回末尾 n 个键（等价 Python list(data)[-n:]）。
func (o *OMap) LastKeys(n int) []string {
	keys := o.Keys()
	if len(keys) <= n {
		return keys
	}
	return keys[len(keys)-n:]
}

func (o *OMap) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteString(", ")
		}
		kb, err := Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteString(": ")
		vb, err := Marshal(o.m[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// Parse 解析 JSON 文本。对象 → *OMap；数组 → []any；数字 → int64/float64；其余标准类型。
// 与 Python json.loads 一样拒绝尾随内容。
func Parse(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	// 拒绝尾随非空白
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return decodeFromToken(dec, tok)
}

func decodeFromToken(dec *json.Decoder, tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := NewOMap()
			for {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				if d, ok := kt.(json.Delim); ok && d == '}' {
					return o, nil
				}
				key, _ := kt.(string)
				val, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(key, val)
			}
		case '[':
			arr := []any{}
			for {
				it, err := dec.Token()
				if err != nil {
					return nil, err
				}
				if d, ok := it.(json.Delim); ok && d == ']' {
					return arr, nil
				}
				val, err := decodeFromToken(dec, it)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	case json.Number:
		if i, err := strconv.ParseInt(t.String(), 10, 64); err == nil {
			return i, nil
		}
		f, err := t.Float64()
		if err != nil {
			return nil, err
		}
		return f, nil
	default:
		return tok, nil // string / bool / nil
	}
}

// Marshal 序列化任意值（支持 *OMap / map[string]any（键排序，等价 sort_keys=True）/ 标准类型）。
// 不转义 < > &（等价 ensure_ascii=False 对 HTML 字符的行为）。
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := encodeValue(&buf, enc, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeValue(buf *bytes.Buffer, enc *json.Encoder, v any) error {
	switch t := v.(type) {
	case *OMap:
		return t.MarshalJSONErr(buf)
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteString(", ")
			}
			if err := writeJSONString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := encodeValue(buf, enc, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []string:
		buf.WriteByte('[')
		for i, s := range t {
			if i > 0 {
				buf.WriteString(", ")
			}
			if err := writeJSONString(buf, s); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				buf.WriteString(", ")
			}
			if err := encodeValue(buf, enc, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	default:
		// 标量与其它结构体：标准编码器（已关 HTML 转义）；Encode 会追加换行，需去除
		if err := enc.Encode(v); err != nil {
			return err
		}
		b := buf.Bytes()
		if n := len(b); n > 0 && b[n-1] == '\n' {
			buf.Truncate(n - 1)
		}
		return nil
	}
}

// MarshalJSONErr 直接写入 buf（Encoder.Encode 会追加换行，故手工实现 OMap 分支）。
func (o *OMap) MarshalJSONErr(buf *bytes.Buffer) error {
	b, err := o.MarshalJSON()
	if err != nil {
		return err
	}
	buf.Write(b)
	return nil
}

func writeJSONString(buf *bytes.Buffer, s string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// json.Marshal 会转义 <>&，这里重写为不转义版本
	out := string(b)
	out = strings.ReplaceAll(out, `\u003c`, "<")
	out = strings.ReplaceAll(out, `\u003e`, ">")
	out = strings.ReplaceAll(out, `\u0026`, "&")
	buf.WriteString(out)
	return nil
}

// ValidJSON 报告文本是否为合法 JSON（用于宽松解析的严格首试）。
func ValidJSON(text string) bool {
	_, err := Parse(text)
	return err == nil
}

// RuneLen 返回 Unicode 码点数（等价 Python len(str)）。
func RuneLen(s string) int { return utf8.RuneCountInString(s) }
