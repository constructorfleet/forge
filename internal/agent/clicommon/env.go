package clicommon

import "os"

// SanitizedEnv builds the environment passed to a CLI backend's subprocess
// from allowed, authVars (a backend's default auth variables), and extra (an
// Adapter's opt-in passthrough) only, looking each up in Forge's own process
// environment. Anything not named in one of those three sets — including
// secrets such as tracker or CI tokens — never reaches the Agent. Mirrors
// internal/agent/claude's sanitizedEnv, generalized so every CLI Agent
// Adapter shares one implementation.
func SanitizedEnv(allowed, authVars, extra []string) []string {
	capacity := len(allowed) + len(authVars) + len(extra)
	seen := make(map[string]bool, capacity)
	keys := make([]string, 0, capacity)
	for _, group := range [][]string{allowed, authVars, extra} {
		for _, key := range group {
			if seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	return env
}
