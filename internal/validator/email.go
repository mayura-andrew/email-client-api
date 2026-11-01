package validator

import "strings"

const emailLocalChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.!#$%&'*+/=?^_`{|}~-"

// EmailProblem explains why address is not a deliverable-looking email, or
// returns "" when it is acceptable. The admin UI implements the same rules
// (web/lib/csv.ts) so its preview matches what the API imports.
func EmailProblem(address string) string {
	switch {
	case address == "":
		return "Email is empty"
	case strings.ContainsAny(address, " \t"):
		return "Email contains spaces"
	case strings.Count(address, "@") == 0:
		return "Email is missing @"
	case strings.Count(address, "@") > 1:
		return "Email has more than one @"
	case len(address) > 254:
		return "Email is too long"
	}
	local, domain, _ := strings.Cut(address, "@")
	switch {
	case local == "":
		return "Nothing before the @"
	case len(local) > 64:
		return "Part before the @ is too long"
	case strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, ".."):
		return "Misplaced dot before the @"
	case strings.Trim(local, emailLocalChars) != "":
		return "Invalid character before the @"
	case domain == "":
		return "Nothing after the @"
	case !strings.Contains(domain, "."):
		return "Domain is missing a dot (e.g. gmail.com)"
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" {
			return "Misplaced dot in the domain"
		}
		if len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") ||
			strings.Trim(strings.ToLower(label), "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			return "Invalid domain"
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 || strings.Trim(strings.ToLower(tld), "abcdefghijklmnopqrstuvwxyz") != "" {
		return "Domain ending is not valid"
	}
	return ""
}
