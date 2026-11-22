package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

func TestBearerToken(t *testing.T) {
	const jwt = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIxIn0.c2ln"
	for name, c := range map[string]struct {
		header []string
		token  string
		res    authResult
	}{
		"none":          {nil, "", authMissing},
		"basic":         {[]string{"Basic Zml4dHVyZQ=="}, "", authMissing},
		"scheme prefix": {[]string{"Bearerx " + jwt}, "", authMissing},
		"no space":      {[]string{"Bearer"}, "", authMalformed},
		"empty":         {[]string{"Bearer "}, "", authMalformed},
		"only padding":  {[]string{"Bearer =="}, "", authMalformed},
		"two tokens":    {[]string{"Bearer a b"}, "", authMalformed},
		"inner =":       {[]string{"Bearer a=b"}, "", authMalformed},
		"bad char":      {[]string{"Bearer a,b"}, "", authMalformed},
		"non-ASCII":     {[]string{"Bearer é"}, "", authMalformed},
		"twice":         {[]string{"Bearer " + jwt, "Bearer " + jwt}, "", authMalformed},
		"too long":      {[]string{"Bearer " + strings.Repeat("a", MaxAuthorizationBytes)}, "", authMalformed},
		"jwt":           {[]string{"Bearer " + jwt}, jwt, authOK},
		"lower case":    {[]string{"bearer " + jwt}, jwt, authOK},
		"spaces":        {[]string{"BEARER   " + jwt}, jwt, authOK},
		"token68 chars": {[]string{"Bearer aZ09-._~+/=="}, "aZ09-._~+/==", authOK},
		"at the limit": {[]string{"Bearer " + strings.Repeat("a", MaxAuthorizationBytes-len("Bearer "))},
			strings.Repeat("a", MaxAuthorizationBytes-len("Bearer ")), authOK},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for _, v := range c.header {
			r.Header.Add("Authorization", v)
		}
		token, res := bearerToken(r)
		assert.Equal(t, c.res, res, name)
		assert.Equal(t, c.token, token, name)
	}
}

// Outside RequireAuth a handler gets no principal, only the 401.
func TestPrincipalOutsideRequireAuth(t *testing.T) {
	_, err := Principal(httptest.NewRequest(http.MethodGet, "/", nil))
	p, _ := ProblemFor(err)
	assert.Equal(t, dto.TypeUnauthorized, p.Type)
	assert.Equal(t, DetailNoAccessToken.Message(), p.Detail)

	// Even a state that recorded a refusal has no principal.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	st := &reqState{auth: authInvalid, principal: controllers.Principal{UserID: 1, SessionID: 1}}
	r = r.WithContext(context.WithValue(r.Context(), stateKey{}, st))
	_, err = Principal(r)
	require.Error(t, err)
}
