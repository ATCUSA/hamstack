package catalog

import "sort"

// Closure returns names plus everything they require, directly or through other requirements, with each
// program after the ones it needs (so installing in order works). added says why each extra one is there.
func Closure(names []string) (ordered []string, added map[string]string) {
	added = map[string]string{}
	seen := map[string]bool{}
	wanted := map[string]bool{}
	for _, n := range names {
		wanted[n] = true
	}
	var visit func(n, why string)
	visit = func(n, why string) {
		if seen[n] {
			return
		}
		seen[n] = true
		p, ok := ByName(n)
		if !ok {
			return
		}
		for _, r := range p.Requires {
			visit(r, n)
		}
		ordered = append(ordered, n)
		if !wanted[n] {
			added[n] = "needed by " + why
		}
	}
	for _, n := range names {
		visit(n, "")
	}
	return ordered, added
}

// Suggestions returns the programs that the chosen ones suggest and that are not already chosen, each once,
// with the program that suggests it. Conflicting ones are left out.
func Suggestions(chosen []string) (names []string, from map[string]string) {
	have := map[string]bool{}
	for _, n := range chosen {
		have[n] = true
	}
	from = map[string]string{}
	for _, n := range chosen {
		p, ok := ByName(n)
		if !ok {
			continue
		}
		for _, s := range p.Suggests {
			if have[s] || from[s] != "" {
				continue
			}
			if c := conflictOf(s, chosen); c != "" {
				continue
			}
			from[s] = n
			names = append(names, s)
		}
	}
	return names, from
}

// conflictOf returns a chosen program that cannot be installed together with name, or "".
func conflictOf(name string, chosen []string) string {
	p, _ := ByName(name)
	for _, c := range chosen {
		q, _ := ByName(c)
		for _, x := range p.Conflicts {
			if x == c {
				return c
			}
		}
		for _, x := range q.Conflicts {
			if x == name {
				return c
			}
		}
	}
	return ""
}

// Conflicts returns the pairs among chosen that cannot be installed together.
func Conflicts(chosen []string) [][2]string {
	var out [][2]string
	for i, a := range chosen {
		for _, b := range chosen[i+1:] {
			if conflictOf(a, []string{b}) != "" {
				out = append(out, [2]string{a, b})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0]+out[i][1] < out[j][0]+out[j][1] })
	return out
}
