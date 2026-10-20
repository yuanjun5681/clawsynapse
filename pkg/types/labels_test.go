package types

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNormalizeLabelsDedupesSortsAndTrims(t *testing.T) {
	in := Labels{
		"env":  LabelString("  production  "),
		"caps": LabelSet([]string{"task.stream", "chat", "chat", " file.receive "}),
	}
	out, err := NormalizeLabels(in)
	if err != nil {
		t.Fatalf("NormalizeLabels: %v", err)
	}
	if s, ok := out["env"].Scalar(); !ok || s != "production" {
		t.Fatalf("env = %#v, want scalar production", out["env"])
	}
	caps := out.Caps()
	if len(caps) != 3 || caps[0] != "chat" || caps[1] != "file.receive" || caps[2] != "task.stream" {
		t.Fatalf("caps = %#v, want sorted deduped set", caps)
	}
}

func TestNormalizeLabelsDropsEmptyEntries(t *testing.T) {
	out, err := NormalizeLabels(Labels{"empty": LabelSet(nil)})
	if err != nil {
		t.Fatalf("NormalizeLabels: %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil labels, got %#v", out)
	}
}

func TestNormalizeLabelsRejectsInvalid(t *testing.T) {
	cases := map[string]Labels{
		"caps as scalar":     {LabelKeyCaps: LabelString("chat")},
		"control char value": {"env": LabelString("prod\x07")},
		"empty value":        {"env": LabelString("   ")},
		"empty key":          {"": LabelString("x")},
		"bad key char":       {"ev il": LabelString("x")},
		"leading dot key":    {".env": LabelString("x")},
		"trailing dash key":  {"env-": LabelString("x")},
	}
	for name, in := range cases {
		if _, err := NormalizeLabels(in); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}

	tooMany := Labels{}
	for i := range MaxLabelEntries + 1 {
		tooMany[string(rune('a'+i%26))+string(rune('a'+i/26))] = LabelString("x")
	}
	if _, err := NormalizeLabels(tooMany); err == nil {
		t.Fatal("expected entry-count limit error")
	}

	oversize := Labels{"big": LabelString(strings.Repeat("x", MaxLabelValueLen+1))}
	if _, err := NormalizeLabels(oversize); err == nil {
		t.Fatal("expected value-length limit error")
	}

	bigSet := Labels{"s": LabelSet(make([]string, MaxLabelSetSize+1))}
	for i := range bigSet["s"].set {
		bigSet["s"].set[i] = strings.Repeat("x", 1) + strings.Repeat("0", i%3)
	}
	if _, err := NormalizeLabels(bigSet); err == nil {
		t.Fatal("expected set-size limit error")
	}
}

func TestLabelValueJSONRoundTripPreservesKind(t *testing.T) {
	in := Labels{"env": LabelString("prod"), "caps": LabelSet([]string{"chat"})}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Labels
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["env"].IsSet() {
		t.Fatal("scalar env decoded as set")
	}
	if !out["caps"].IsSet() {
		t.Fatal("set caps decoded as scalar")
	}
}

func TestLabelValueJSONRejectsNonString(t *testing.T) {
	var v LabelValue
	if err := json.Unmarshal([]byte(`42`), &v); err == nil {
		t.Fatal("expected error for numeric label value")
	}
	if err := json.Unmarshal([]byte(`{"a":1}`), &v); err == nil {
		t.Fatal("expected error for object label value")
	}
}

func TestLabelValueYAMLRoundTrip(t *testing.T) {
	doc := "labels:\n  env: prod\n  caps:\n    - chat\n    - task.stream\n"
	var parsed struct {
		Labels Labels `yaml:"labels"`
	}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if s, ok := parsed.Labels["env"].Scalar(); !ok || s != "prod" {
		t.Fatalf("env = %#v", parsed.Labels["env"])
	}
	if !parsed.Labels["caps"].IsSet() {
		t.Fatal("caps should be a set")
	}

	out, err := yaml.Marshal(struct {
		Labels Labels `yaml:"labels"`
	}{Labels: parsed.Labels})
	if err != nil {
		t.Fatalf("yaml marshal: %v", err)
	}
	var again struct {
		Labels Labels `yaml:"labels"`
	}
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("yaml round-trip: %v", err)
	}
	if !again.Labels["caps"].IsSet() || len(again.Labels["caps"].Members()) != 2 {
		t.Fatalf("round-trip caps = %#v", again.Labels["caps"])
	}
}

func TestLabelMatchingDistinguishesScalarFromSet(t *testing.T) {
	l := Labels{"env": LabelString("prod"), "caps": LabelSet([]string{"chat"})}
	if !l.MatchExact("env", "prod") {
		t.Fatal("exact match on scalar failed")
	}
	if l.MatchExact("caps", "chat") {
		t.Fatal("exact match must not hit set members")
	}
	if !l.MatchMember("caps", "chat") {
		t.Fatal("member match on set failed")
	}
	if l.MatchMember("env", "prod") {
		t.Fatal("member match must not hit scalars")
	}
}

func TestValidateDisplayName(t *testing.T) {
	if err := ValidateDisplayName(""); err != nil {
		t.Fatalf("empty display name should be allowed: %v", err)
	}
	if err := ValidateDisplayName("上海现场网关"); err != nil {
		t.Fatalf("unicode display name should be allowed: %v", err)
	}
	if err := ValidateDisplayName("bad\x1b[0m"); err == nil {
		t.Fatal("control characters must be rejected")
	}
	if err := ValidateDisplayName(strings.Repeat("x", MaxDisplayNameLen+1)); err == nil {
		t.Fatal("oversize display name must be rejected")
	}
}

func TestNormalizePeerLocalAttrs(t *testing.T) {
	attrs, err := NormalizePeerLocalAttrs(PeerLocalAttrs{
		Alias:  "  现场网关 ",
		Labels: Labels{"floor": LabelString("3")},
	})
	if err != nil {
		t.Fatalf("NormalizePeerLocalAttrs: %v", err)
	}
	if attrs.Alias != "现场网关" {
		t.Fatalf("alias = %q", attrs.Alias)
	}
	if attrs.IsZero() {
		t.Fatal("attrs should not be zero")
	}
	if (PeerLocalAttrs{}).IsZero() != true {
		t.Fatal("zero attrs must report IsZero")
	}
	if _, err := NormalizePeerLocalAttrs(PeerLocalAttrs{Description: "bad\nline"}); err == nil {
		t.Fatal("control characters in description must be rejected")
	}
}
