package workspace

import "strings"

// PlanPhase groups a Perform line with the Expect lines that follow it until the
// next Perform.
type PlanPhase struct {
	Perform ParsedLine
	Expects []ParsedLine
}

// SplitPlanPhases divides plan lines into phases, each starting with a Perform.
func SplitPlanPhases(lines []ParsedLine) []PlanPhase {
	var phases []PlanPhase
	cur := -1
	for _, l := range lines {
		switch strings.ToLower(l.Action) {
		case "perform":
			phases = append(phases, PlanPhase{Perform: l})
			cur++
		case "expect":
			if cur >= 0 {
				phases[cur].Expects = append(phases[cur].Expects, l)
			}
		}
	}
	return phases
}
