// Package quota is the normalized quota model and the engine that keeps it fresh. Unknown
// numbers are nil pointers, never zero: an explicit 0 is real usage.
package quota

// Window is one usage window such as a 5 h or 7 d allowance.
type Window struct {
	// ID is stable within the provider, e.g. "5h", "7d", "7d-gemini".
	ID string `json:"id"`
	// Short is the compact line label, e.g. "5h", "gem".
	Short string `json:"short"`
	// Label is the panel label.
	Label string `json:"label"`
	// Used is the used fraction in [0,1]; nil when the provider did not report it.
	Used *float64 `json:"used,omitempty"`
	// Value is detail text such as "$41.43 / $48.00".
	Value string `json:"value,omitempty"`
	// ResetAtMs is the epoch-millisecond reset time; nil when unknown.
	ResetAtMs *int64 `json:"resetAtMs,omitempty"`
	// Important windows appear in the condensed line.
	Important bool `json:"important,omitempty"`
	// Group names a window family such as "Gemini Models".
	Group string `json:"group,omitempty"`
	// Period is the reported duration such as "5h", "7d", "1mo".
	Period string `json:"period,omitempty"`
	// Scope names what a non-plan-wide window limits, e.g. "search" or "gemini".
	Scope string `json:"scope,omitempty"`
}

// Balance is a prepaid or capped balance.
type Balance struct {
	Remaining *float64 `json:"remaining,omitempty"`
	Total     *float64 `json:"total,omitempty"`
	Used      *float64 `json:"used,omitempty"`
	// Currency is a symbol or code such as "$".
	Currency string `json:"currency"`
	Detail   string `json:"detail,omitempty"`
}

// Origin says where a snapshot came from.
type Origin string

// Snapshot origins.
const (
	FromEndpoint Origin = "endpoint"
	FromHeaders  Origin = "headers"
)

// Snapshot is one provider's quota at one point in time.
type Snapshot struct {
	// Provider is the PiG provider id.
	Provider  string `json:"provider"`
	FetchedAt int64  `json:"fetchedAt"`
	Source    Origin `json:"source"`
	// Tier is a plan badge such as "max 20x"; never an account or project identifier.
	Tier    string   `json:"tier,omitempty"`
	Credits *Balance `json:"credits,omitempty"`
	Windows []Window `json:"windows,omitempty"`
	// ResetCredits counts on-demand rate-limit resets available now; nil when unreported.
	ResetCredits *int `json:"resetCredits,omitempty"`
	// Error says why the snapshot is incomplete or failed.
	Error string `json:"error,omitempty"`
	// NeedsAuth means credentials are missing and the user has to /login.
	NeedsAuth bool `json:"needsAuth,omitempty"`
}

// Frac returns a pointer to f.
func Frac(f float64) *float64 { return &f }

// Ms returns a pointer to v.
func Ms(v int64) *int64 { return &v }

// Int returns a pointer to v.
func Int(v int) *int { return &v }

// ResetCredit is one on-demand rate-limit reset credit as the provider lists it.
type ResetCredit struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	GrantedAtMs  *int64 `json:"grantedAtMs,omitempty"`
	ExpiresAtMs  *int64 `json:"expiresAtMs,omitempty"`
	RedeemedAtMs *int64 `json:"redeemedAtMs,omitempty"`
	// SupportedByPlan is nil when the provider did not say.
	SupportedByPlan *bool `json:"supportedByPlan,omitempty"`
}

// ResetList is the provider's reset credit listing.
type ResetList struct {
	Provider  string        `json:"provider"`
	FetchedAt int64         `json:"fetchedAt"`
	Available int           `json:"available"`
	Credits   []ResetCredit `json:"credits"`
}

// ResetCode is the provider's verdict for one consume request; only ResetDone spends a credit.
type ResetCode string

// Reset outcomes.
const (
	ResetDone       ResetCode = "reset"
	NothingToReset  ResetCode = "nothing_to_reset"
	NoCredit        ResetCode = "no_credit"
	AlreadyRedeemed ResetCode = "already_redeemed"
)

// ResetOutcome is the result of one consume request.
type ResetOutcome struct {
	Provider     string    `json:"provider"`
	Code         ResetCode `json:"code"`
	CreditID     string    `json:"creditId"`
	WindowsReset int       `json:"windowsReset"`
}
