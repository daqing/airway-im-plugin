package models

import "time"

// User is the IM identity. It is not managed inside the plugin: rows are
// auto-registered the first time a host-signed credential for (name, uuid)
// authenticates, and display fields refresh whenever a newer credential
// carries different values. UUID is the stable identity; username is the
// current display name.
type User struct {
	ID         int64      `db:"id" json:"id"`
	UUID       string     `db:"uuid" json:"uuid"`
	Username   string     `db:"username" json:"username"`
	Nickname   *string    `db:"nickname" json:"nickname"`
	AvatarURL  *string    `db:"avatar_url" json:"avatar_url"`
	Email        *string    `db:"email" json:"email"`
	LastSeenAt   *time.Time `db:"last_seen_at" json:"last_seen_at"`
	TokenVersion int64      `db:"token_version" json:"-"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at" json:"updated_at"`
}

func (User) TableName() string { return "users" }

func init() { registerREPLModel("User", User{}) }
