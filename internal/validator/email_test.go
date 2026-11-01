package validator

import "testing"

func TestEmailProblem(t *testing.T) {
	valid := []string{"ada@example.org", "first.last+tag@sub.example.co.uk", "o'brien@example.ie", "x@e.io"}
	for _, address := range valid {
		if problem := EmailProblem(address); problem != "" {
			t.Errorf("%q rejected: %s", address, problem)
		}
	}
	invalid := map[string]string{
		"":                      "Email is empty",
		"ada lovelace@x.org":    "Email contains spaces",
		"ada.example.org":       "Email is missing @",
		"a@b@example.org":       "Email has more than one @",
		"@example.org":          "Nothing before the @",
		"ada.@example.org":      "Misplaced dot before the @",
		"ad..a@example.org":     "Misplaced dot before the @",
		"ada(x)@example.org":    "Invalid character before the @",
		"ada@":                  "Nothing after the @",
		"ada@localhost":         "Domain is missing a dot (e.g. gmail.com)",
		"ada@example..org":      "Misplaced dot in the domain",
		"ada@-example.org":      "Invalid domain",
		"ada@example.c":         "Domain ending is not valid",
		"ada@example.123":       "Domain ending is not valid",
		`"unnamed@example.org"`: "Invalid character before the @",
	}
	for address, want := range invalid {
		if got := EmailProblem(address); got != want {
			t.Errorf("EmailProblem(%q) = %q, want %q", address, got, want)
		}
	}
}
