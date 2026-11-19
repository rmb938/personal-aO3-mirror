package controllers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobErrorCodesComplete checks that JobErrorCodes lists every ErrorCode constant declared
// in this package (found by parsing the source), so a new code cannot be left out of the API's
// dto.JobErrorCode set.
func TestJobErrorCodesComplete(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var declared []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "ErrorCode" {
					continue
				}
				for _, v := range vs.Values {
					switch v := v.(type) {
					case *ast.BasicLit:
						s, err := strconv.Unquote(v.Value)
						require.NoError(t, err)
						declared = append(declared, s)
					case *ast.CallExpr: // ErrorCode(httpx.ClassX): resolved below by name
						declared = append(declared, "call:"+selName(v))
					}
				}
			}
		}
	}
	require.NotEmpty(t, declared)

	codes := JobErrorCodes()
	for _, c := range declared {
		if rest, ok := strings.CutPrefix(c, "call:"); ok {
			c = classValues[rest]
			require.NotEmpty(t, c, "unknown conversion %s: add it to classValues", rest)
		}
		assert.Contains(t, codes, c)
	}
}

// classValues resolves the ErrorCode(httpx.ClassX) conversions the parser sees by name.
var classValues = map[string]string{
	"ClassUnexpected": string(CodeParseUnexpected),
	"ClassNotFound":   string(CodeNotFound),
	"ClassRestricted": string(CodeRestricted),
	"ClassHidden":     string(CodeHidden),
	"ClassAdultGate":  string(CodeAdultGate),
}

// selName names the selector inside a conversion like ErrorCode(httpx.ClassX).
func selName(c *ast.CallExpr) string {
	if len(c.Args) == 1 {
		if sel, ok := c.Args[0].(*ast.SelectorExpr); ok {
			return sel.Sel.Name
		}
	}
	return ""
}
