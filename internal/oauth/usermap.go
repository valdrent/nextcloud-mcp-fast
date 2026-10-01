// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package oauth

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// Account holds the Nextcloud credentials for one OAuth user.
type Account struct {
	Host        string `json:"host"`
	Username    string `json:"username"`
	AppPassword string `json:"app_password"`
}

// UserMap maps token subjects (or, as fallback, email / preferred_username)
// to Nextcloud accounts.
type UserMap struct{ m map[string]Account }

// LoadUserMap reads a JSON file of the form {"<sub|email>": {"host":…,"username":…,"app_password":…}}.
// The file holds secrets, so it must not be readable by group or others.
func LoadUserMap(path string) (*UserMap, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("accounts file %s must not be accessible by group/others (chmod 600)", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]Account{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("accounts file %s: %w", path, err)
	}
	for k, a := range m {
		if k == "" || a.Username == "" || a.AppPassword == "" {
			return nil, fmt.Errorf("accounts file %s: entry %q needs username and app_password", path, k)
		}
	}
	return &UserMap{m: m}, nil
}

// Lookup returns the account for an authenticated token, or false when the
// user is not mapped (the caller must then deny access).
func (u *UserMap) Lookup(ti *auth.TokenInfo) (Account, bool) {
	if u == nil || ti == nil {
		return Account{}, false
	}
	if a, ok := u.m[ti.UserID]; ok {
		return a, true
	}
	for _, k := range []string{"email", "preferred_username"} {
		if v, _ := ti.Extra[k].(string); v != "" {
			if a, ok := u.m[strings.ToLower(v)]; ok {
				return a, true
			}
		}
	}
	return Account{}, false
}
