package eval

import "sort"

// Split partitions rows into a train slice and a held-out slice, by
// benchmark category (eval_name) rather than by row — otherwise near-
// duplicate prompts from the same benchmark could land on both sides. The
// split is deterministic: every distinct eval_name, sorted alphabetically,
// with every 5th one (~20%) held out. Since none of AutoRoute's L1 rules or
// L2 exemplars were tuned against RouterBench, this first split's numbers
// are already honest by construction — it exists as standing infrastructure
// so that stays true if thresholds/exemplars are ever tuned against the
// train slice later.
func Split(rows []Row) (train, heldOut []Row) {
	seen := make(map[string]bool)
	var names []string
	for _, r := range rows {
		if !seen[r.EvalName] {
			seen[r.EvalName] = true
			names = append(names, r.EvalName)
		}
	}
	sort.Strings(names)

	isHeldOut := make(map[string]bool, len(names))
	for i, name := range names {
		if i%5 == 0 {
			isHeldOut[name] = true
		}
	}

	for _, r := range rows {
		if isHeldOut[r.EvalName] {
			heldOut = append(heldOut, r)
		} else {
			train = append(train, r)
		}
	}
	return train, heldOut
}
