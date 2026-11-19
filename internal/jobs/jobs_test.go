package jobs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKindsComplete checks that Kinds lists every Kind* constant in jobs.go (found by parsing
// the source), so a new kind cannot be left out of the API's dto.JobKind set.
func TestKindsComplete(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "jobs.go", nil, 0)
	require.NoError(t, err)
	var declared []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Kind") {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				require.True(t, ok, "%s is not a string literal", name.Name)
				s, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				declared = append(declared, s)
			}
		}
	}
	assert.ElementsMatch(t, declared, Kinds())
}
