// Package moderation screens message content before it is delivered, and
// tracks the labels an operator attaches to a sender. The word list is not
// kept here: the moderation service owns it and answers over HTTP, so this
// package holds the call to it and the label bookkeeping around the review
// queue.
package moderation

// LabelIllegal is the single wire label the review flow uses today: a
// message a client screened out locally carries it, and rejecting a queued
// message attaches it to the sender, so their later messages are queued too.
const LabelIllegal = "illegal"

// LabelBanned marks a permanently banned account: authentication fails for
// every credential presented by the user (see auth.UserFromHeader), the host
// refuses to mint new ones, and their live connections are kicked when the
// label is attached.
const LabelBanned = "banned"

// Review queue states (message_reviews.status).
const (
	ReviewPending  = "pending"
	ReviewApproved = "approved"
	ReviewRejected = "rejected"
)
