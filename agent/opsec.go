package agent

import "github.com/Autumn-27/artex/locale"

// Engagement profiles. ProfilePentest is the historical client-pentest behaviour
// and the default everywhere; ProfileOp is a live-fire simulation with an
// observing blue team, where retained access matters more than coverage.
const (
	ProfilePentest = "pentest"
	ProfileOp      = "op"
)

// normalizeProfile maps an arbitrary profile value onto a known profile. An
// empty or unknown value means pentest, so callers that never thread a profile
// keep today's prompts unchanged.
func normalizeProfile(profile string) string {
	if profile == ProfileOp {
		return ProfileOp
	}
	return ProfilePentest
}

// resolveProfile reads a profile resolver. A nil resolver is pentest, so a task
// without a profile behaves exactly as before.
func resolveProfile(fn func() string) string {
	if fn == nil {
		return ProfilePentest
	}
	return normalizeProfile(fn())
}

// opsecBlock is section [D], the code-owned stealth tail appended to every role
// prompt AFTER the operation-constraint block and BEFORE the output-language
// instruction. It is empty for pentest so those prompts keep their historical
// shape. The text is static (no timestamps or per-task values) because the whole
// system prompt is the provider cache prefix.
func opsecBlock(profile string, langs ...locale.Lang) string {
	if normalizeProfile(profile) != ProfileOp {
		return ""
	}
	return locale.Text(locale.First(langs), opsecBlockText)
}

// opsecBlockText is the English op block. Each bullet is imperative: it tells a
// worker what to weigh before spending a request, not what a report should say.
const opsecBlockText = "\n\n[Stealth operation: a blue team is watching CloudTrail, WAF, EDR, GitHub and mail. Access that is noticed is a partial failure]\n- This is a live-fire simulation. Hold access quietly instead of maximizing coverage.\n- Before each action ask: would this look like normal work here?\n- Conform to the house style. Never invent external i<NNNN>-shaped names.\n- Reuse before creating: an existing service user, group, security group, subnet, instance profile, keypair or bucket prefix.\n- Record every created object in the residue ledger as soon as it exists.\n- A push that triggers their pipeline is a human notification; record the side effect first.\n- Prefer one request that answers three questions over three requests.\n- When detected (429 burst, captcha, WAF block, AccessDenied spike): stop, back off, rotate identity.\n- Keep every HTTP request on the egress proxy; curl, python and node inherit it.\n- Batch validation: cite captured traffic instead of re-probing an unchanged target.\n- Leave no obvious marker: temporary users, probe workflows and scratch files are residue too."
