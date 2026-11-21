package searchquery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTerms(t *testing.T) {
	w := func(s string) Term { return Term{Text: s} }
	x := func(s string) Term { return Term{Text: s, Exclude: true} }
	privateUse := string(rune(0xE000))
	for q, want := range map[string][]Term{
		"":                              nil,
		"   \t\n":                       nil,
		"lorem":                         {w("lorem")},
		"  lorem   ipsum ":              {w("lorem"), w("ipsum")},
		`"lorem  ipsum" dolor`:          {w("lorem ipsum"), w("dolor")},
		`lorem "ipsum dolor`:            {w("lorem"), w("ipsum dolor")}, // unclosed quote
		`lorem"ipsum dolor"sit`:         {w("lorem"), w("ipsum dolor"), w("sit")},
		`-lorem ipsum`:                  {x("lorem"), w("ipsum")},
		`ipsum -"lorem dolor"`:          {w("ipsum"), x("lorem dolor")},
		`lorem-ipsum`:                   {w("lorem-ipsum")}, // only a leading '-' excludes
		`- lorem`:                       {w("lorem")},
		`--lorem`:                       {x("-lorem")},
		`- -- "" " " !!! ... ipsum`:     {w("ipsum")}, // no letters or digits
		`title:lorem OR ipsum* NEAR(a)`: {w("title:lorem"), w("OR"), w("ipsum*"), w("NEAR(a)")},
		"lorem 42 " + privateUse:        {w("lorem"), w("42"), w(privateUse)},
	} {
		assert.Equal(t, want, Terms(q), "%q", q)
	}
}
