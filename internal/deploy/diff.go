package deploy

import "strings"

// DiffLine is one line of a line-by-line comparison of two configs.
type DiffLine struct {
	Op   string `json:"op"` // " " unchanged, "-" removed, "+" added
	Text string `json:"text"`
}

// Diff compares two texts line by line (longest common subsequence). Configs are small,
// so the simple quadratic algorithm is fine.
func Diff(old, new string) []DiffLine {
	a, b := splitLines(old), splitLines(new)
	// lcs[i][j] is the LCS length of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []DiffLine
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{" ", a[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{"-", a[i]})
			i++
		default:
			out = append(out, DiffLine{"+", b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, DiffLine{"-", a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, DiffLine{"+", b[j]})
	}
	return out
}

// Changed reports whether a diff contains any change.
func Changed(lines []DiffLine) bool {
	for _, l := range lines {
		if l.Op != " " {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
