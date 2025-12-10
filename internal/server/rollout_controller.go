package server

import (
	"hash/fnv"
	"net/http"
	"slices"
)

const RolloutCookieName = "kamal-rollout"

type RolloutController struct {
	Percentage           int      `json:"percentage"`
	PercentageSplitPoint float64  `json:"percentage_split_point"`
	Allowlist            []string `json:"allowlist"`
}

// NewRolloutController will initialize the *RolloutController with the params
// but also setup the split percentage.
// It does that by computing the maximum 32-bit hash value and converting it to
// float64 for precision, based on the percentage param.
func NewRolloutController(percentage int, allowlist []string) *RolloutController {
	maxHashValue := float64(uint32(0xFFFFFFFF))
	percentageSplitPoint := maxHashValue * (float64(percentage) / 100.0)

	return &RolloutController{
		Percentage:           percentage,
		PercentageSplitPoint: percentageSplitPoint,
		Allowlist:            allowlist,
	}
}

// RequestUsesRolloutGroup checks if the user is in the rollout group.
// It does that by checking if he's in the allow list or in the rollout
// percentage. A cookie is used to determine both things.
func (rc *RolloutController) RequestUsesRolloutGroup(r *http.Request) bool {
	splitValue := rc.splitValue(r)
	if splitValue == "" {
		return false
	}

	if rc.valueInAllowlist(splitValue) {
		return true
	}

	return rc.valueInRolloutPercentage(splitValue)
}

func (rc *RolloutController) valueInAllowlist(value string) bool {
	return slices.Contains(rc.Allowlist, value)
}

func (rc *RolloutController) valueInRolloutPercentage(value string) bool {
	hash := rc.hashForValue(value)
	return float64(hash) <= rc.PercentageSplitPoint
}

func (rc *RolloutController) hashForValue(value string) uint32 {
	hasher := fnv.New32a()
	hasher.Write([]byte(value))
	return hasher.Sum32()
}

func (rc *RolloutController) splitValue(r *http.Request) string {
	cookie, err := r.Cookie(RolloutCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
