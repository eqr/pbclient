package pbclient

import "testing"

func TestComparisons(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Eq", Eq("name", "john"), "name='john'"},
		{"EqEscapes", Eq("name", "o'hara"), "name='o\\'hara'"},
		{"Neq", Neq("status", "inactive"), "status!='inactive'"},
		{"Gt", Gt("age", "30"), "age>30"},
		{"Gte trims", Gte("age", " 42 "), "age>=42"},
		{"Lt", Lt("score", "100"), "score<100"},
		{"Lte", Lte("score", "5"), "score<=5"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestLogicalOperators(t *testing.T) {
	if got := And("a=1", "b=2"); got != "(a=1 && b=2)" {
		t.Fatalf("And unexpected value: %q", got)
	}

	if got := Or("x>0"); got != "x>0" {
		t.Fatalf("Or with single filter expected passthrough, got %q", got)
	}

	if got := And("  ", "", "value!=null"); got != "value!=null" {
		t.Fatalf("And should skip empty filters, got %q", got)
	}

	if got := Or(); got != "" {
		t.Fatalf("Or with no filters expected empty string, got %q", got)
	}
}
