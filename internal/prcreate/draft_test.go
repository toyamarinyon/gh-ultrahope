package prcreate

import "testing"

func boolPtr(v bool) *bool { return &v }

func TestResolveDraft_UsesCLIWhenGiven(t *testing.T) {
	cfg := FileConfig{
		Create: CreateConfig{
			Draft: boolPtr(false),
		},
	}

	if got := resolveDraft(Options{DraftGiven: true, Draft: true}, cfg); got != true {
		t.Fatalf("expected draft=true when CLI given true, got %v", got)
	}
	if got := resolveDraft(Options{DraftGiven: true, Draft: false}, cfg); got != false {
		t.Fatalf("expected draft=false when CLI given false, got %v", got)
	}
}

func TestResolveDraft_FallsBackToConfigWhenNotGiven(t *testing.T) {
	cfgTrue := FileConfig{
		Create: CreateConfig{
			Draft: boolPtr(true),
		},
	}
	if got := resolveDraft(Options{DraftGiven: false, Draft: false}, cfgTrue); got != true {
		t.Fatalf("expected draft=true from config when CLI not given, got %v", got)
	}

	cfgNil := FileConfig{Create: CreateConfig{Draft: nil}}
	if got := resolveDraft(Options{DraftGiven: false, Draft: false}, cfgNil); got != false {
		t.Fatalf("expected draft=false default when neither CLI nor config set, got %v", got)
	}
}
