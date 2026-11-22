// Package auth holds the authentication primitives for the API (Part 2, stage 16): password
// hashing and policy, TOTP, the auth.key secrets, opaque tokens and recovery codes, and access
// JWTs. It has no SQL and no HTTP: the Auth controller combines these with the users.db store,
// and the API layer turns its results into responses.
//
// Nothing here logs. Errors never carry passwords, codes, secrets or tokens.
package auth
