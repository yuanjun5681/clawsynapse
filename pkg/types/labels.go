package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Node attribute limits. Shared by config validation, discovery announce
// publishing and validation, and the local peer-attrs API.
const (
	LabelKeyCaps = "caps"

	MaxDisplayNameLen = 128
	MaxLabelEntries   = 32
	MaxLabelKeyLen    = 64
	MaxLabelValueLen  = 256
	MaxLabelSetSize   = 32
	MaxLabelsBytes    = 4096

	MaxPeerAliasLen       = 128
	MaxPeerDescriptionLen = 512
)

// LabelValue is either a single string or an unordered set of strings. The
// distinction is observable: exact-match queries only hit scalars, member
// queries only hit sets. Reserved key "caps" must be a set.
type LabelValue struct {
	scalar string
	set    []string
	isSet  bool
}

func LabelString(s string) LabelValue { return LabelValue{scalar: s} }

func LabelSet(values []string) LabelValue {
	return LabelValue{set: append([]string(nil), values...), isSet: true}
}

func (v LabelValue) IsSet() bool { return v.isSet }

func (v LabelValue) Scalar() (string, bool) {
	if v.isSet {
		return "", false
	}
	return v.scalar, true
}

// Members returns the scalar as a single-element slice or a copy of the set.
func (v LabelValue) Members() []string {
	if !v.isSet {
		if v.scalar == "" {
			return nil
		}
		return []string{v.scalar}
	}
	return append([]string(nil), v.set...)
}

// MatchExact reports scalar equality; never matches sets.
func (v LabelValue) MatchExact(s string) bool { return !v.isSet && v.scalar == s }

// MatchMember reports set membership; never matches scalars.
func (v LabelValue) MatchMember(s string) bool {
	if !v.isSet {
		return false
	}
	for _, m := range v.set {
		if m == s {
			return true
		}
	}
	return false
}

func (v LabelValue) MarshalJSON() ([]byte, error) {
	if v.isSet {
		if v.set == nil {
			return []byte("[]"), nil
		}
		return json.Marshal(v.set)
	}
	return json.Marshal(v.scalar)
}

func (v *LabelValue) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("label value is empty")
	}
	if trimmed[0] == '[' {
		var set []string
		if err := json.Unmarshal(trimmed, &set); err != nil {
			return fmt.Errorf("label value must be a string or an array of strings: %w", err)
		}
		*v = LabelSet(set)
		return nil
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return fmt.Errorf("label value must be a string or an array of strings: %w", err)
	}
	*v = LabelString(s)
	return nil
}

func (v LabelValue) MarshalYAML() (any, error) {
	if v.isSet {
		if v.set == nil {
			return []string{}, nil
		}
		return v.set, nil
	}
	return v.scalar, nil
}

func (v *LabelValue) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		var set []string
		if err := node.Decode(&set); err != nil {
			return fmt.Errorf("label value must be a string or a list of strings: %w", err)
		}
		*v = LabelSet(set)
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		var s string
		if err := node.Decode(&s); err != nil {
			return fmt.Errorf("label value must be a string or a list of strings: %w", err)
		}
		*v = LabelString(s)
		return nil
	}
	return fmt.Errorf("label value must be a string or a list of strings")
}

// Labels maps a key to a scalar or a set of strings.
type Labels map[string]LabelValue

// Caps returns a copy of the reserved "caps" set, or nil when absent.
func (l Labels) Caps() []string {
	v, ok := l[LabelKeyCaps]
	if !ok || !v.isSet {
		return nil
	}
	return append([]string(nil), v.set...)
}

// WithCaps returns a copy of l with the reserved "caps" set replaced.
func (l Labels) WithCaps(caps []string) Labels {
	out := make(Labels, len(l)+1)
	for k, v := range l {
		out[k] = v
	}
	out[LabelKeyCaps] = LabelSet(caps)
	return out
}

func (l Labels) MatchExact(key, value string) bool {
	v, ok := l[key]
	return ok && v.MatchExact(value)
}

func (l Labels) MatchMember(key, value string) bool {
	v, ok := l[key]
	return ok && v.MatchMember(value)
}

// NormalizeLabels validates and canonicalizes labels: trims keys and values,
// rejects invalid characters and size violations, enforces the reserved
// "caps" type, dedupes and sorts sets, and drops empty entries. The result
// is stable for signing and comparison.
func NormalizeLabels(in Labels) (Labels, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > MaxLabelEntries {
		return nil, fmt.Errorf("labels: at most %d entries allowed", MaxLabelEntries)
	}

	out := make(Labels, len(in))
	total := 0
	for rawKey, value := range in {
		key := strings.TrimSpace(rawKey)
		if err := validateLabelKey(key); err != nil {
			return nil, err
		}
		total += len(key)

		if value.isSet {
			if len(value.set) > MaxLabelSetSize {
				return nil, fmt.Errorf("label %q: at most %d elements allowed", key, MaxLabelSetSize)
			}
			seen := make(map[string]struct{}, len(value.set))
			set := make([]string, 0, len(value.set))
			for _, rawMember := range value.set {
				member := strings.TrimSpace(rawMember)
				if err := validateLabelValue(key, member); err != nil {
					return nil, err
				}
				if _, dup := seen[member]; dup {
					continue
				}
				seen[member] = struct{}{}
				set = append(set, member)
				total += len(member)
			}
			if len(set) == 0 {
				continue
			}
			sort.Strings(set)
			out[key] = LabelSet(set)
			continue
		}

		if key == LabelKeyCaps {
			return nil, fmt.Errorf("label %q is reserved and must be an array of strings", LabelKeyCaps)
		}
		scalar := strings.TrimSpace(value.scalar)
		if err := validateLabelValue(key, scalar); err != nil {
			return nil, err
		}
		total += len(scalar)
		out[key] = LabelString(scalar)
	}

	if total > MaxLabelsBytes {
		return nil, fmt.Errorf("labels: total size exceeds %d bytes", MaxLabelsBytes)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func validateLabelKey(key string) error {
	if key == "" {
		return fmt.Errorf("labels: key must not be empty")
	}
	if len(key) > MaxLabelKeyLen {
		return fmt.Errorf("label key %q exceeds %d bytes", key, MaxLabelKeyLen)
	}
	for i, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
			if i == 0 || i == len(key)-1 {
				return fmt.Errorf("label key %q must start and end with a letter or digit", key)
			}
		default:
			return fmt.Errorf("label key %q contains invalid character %q", key, r)
		}
	}
	return nil
}

func validateLabelValue(key, value string) error {
	if value == "" {
		return fmt.Errorf("label %q: value must not be empty", key)
	}
	if len(value) > MaxLabelValueLen {
		return fmt.Errorf("label %q: value exceeds %d bytes", key, MaxLabelValueLen)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("label %q: value is not valid UTF-8", key)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("label %q: value contains control characters", key)
	}
	return nil
}

// ValidateDisplayName checks a node display name or peer alias. Empty is
// valid and means unset.
func ValidateDisplayName(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > MaxDisplayNameLen {
		return fmt.Errorf("display name exceeds %d bytes", MaxDisplayNameLen)
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("display name is not valid UTF-8")
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fmt.Errorf("display name contains control characters")
	}
	return nil
}

// ValidatePeerText checks a local peer alias or description.
func ValidatePeerText(field, value string, maxLen int) error {
	if value == "" {
		return nil
	}
	if len(value) > maxLen {
		return fmt.Errorf("%s exceeds %d bytes", field, maxLen)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", field)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s contains control characters", field)
	}
	return nil
}

// NormalizePeerLocalAttrs validates and canonicalizes locally maintained
// remarks about a peer. Zero value means "no local remarks".
func NormalizePeerLocalAttrs(in PeerLocalAttrs) (PeerLocalAttrs, error) {
	out := PeerLocalAttrs{
		Alias:       strings.TrimSpace(in.Alias),
		Description: strings.TrimSpace(in.Description),
	}
	if err := ValidatePeerText("alias", out.Alias, MaxPeerAliasLen); err != nil {
		return PeerLocalAttrs{}, err
	}
	if err := ValidatePeerText("description", out.Description, MaxPeerDescriptionLen); err != nil {
		return PeerLocalAttrs{}, err
	}
	labels, err := NormalizeLabels(in.Labels)
	if err != nil {
		return PeerLocalAttrs{}, err
	}
	out.Labels = labels
	return out, nil
}

// IsZero reports whether any local remark is set.
func (a PeerLocalAttrs) IsZero() bool {
	return a.Alias == "" && a.Description == "" && len(a.Labels) == 0
}
