package protect

// typePriority ranks smart-detect types by how specific/useful they are to
// show as a clip's single "headline" type, most specific first. Protect's
// own smartDetectTypes arrays are alphabetically ordered, not
// priority-ordered — naively taking element [0] means whichever type
// starts earliest in the alphabet always wins regardless of relevance
// (confirmed: "face" beat "licensePlate" and "vehicle" on every single
// co-occurring event, purely because 'f' < 'l' < 'v').
var typePriority = map[string]int{
	"licensePlate": 0,
	"face":         1,
	"person":       2,
	"vehicle":      3,
	"animal":       4,
}

// PickPrimaryType chooses the single most useful type to show from a
// smart-detect event's (possibly several, simultaneous) classifications.
// Anything not in typePriority (audio alarms like "alrmSpeak", or an
// unrecognized future type) ranks below all of them but is still returned
// if it's all there is.
func PickPrimaryType(types []string) string {
	best := ""
	bestRank := len(typePriority) + 1
	for _, t := range types {
		rank, known := typePriority[t]
		if !known {
			rank = len(typePriority)
		}
		if best == "" || rank < bestRank {
			best = t
			bestRank = rank
		}
	}
	return best
}
