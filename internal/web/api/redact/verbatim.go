package redact

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"regexp/syntax"
)

// Verbatim is a string type for server-generated or strictly shaped values that are never
// fanwork (tokens, TOTP secrets, recovery codes, usernames, invite URLs). A DTO field whose type
// (after pointers, slices and arrays) implements it passes a value unhashed only when the whole
// value matches VerbatimPattern. Any other value fails the response with a *PatternError (a
// 500), in both modes: the field is never hashed, so failing closed is the only safe outcome.
// DTO packages satisfy it structurally and never import redact.
//
// A pattern may use only literals, character classes, groups, alternation, ? and bounded
// {n,m} repetition, over printable ASCII without the space (0x21..0x7E), and must match at most
// maxVerbatimLen bytes and never the empty string. No '.', '*', '+', anchors or case folding:
// a value that can't hold a space or exceed a few hundred characters can't carry prose.
type Verbatim interface {
	VerbatimPattern() string
}

var verbatimType = reflect.TypeFor[Verbatim]()

// maxVerbatimLen bounds what a pattern may match (an access JWT is the longest, ~400 bytes).
const maxVerbatimLen = 1024

// PatternError reports a value that does not match its Verbatim field's pattern. It names only
// the Go field, never the value.
type PatternError struct {
	Path string
}

func (e *PatternError) Error() string {
	return "redact: value does not match the pattern of " + e.Path
}

// compileVerbatim checks Verbatim type t and returns its pattern anchored to the whole value.
func compileVerbatim(t reflect.Type, path string) (*regexp.Regexp, error) {
	if t.Kind() != reflect.String {
		return nil, fmt.Errorf("redact: %s: Verbatim type %s has kind %s, not string", path, t, t.Kind())
	}
	if implements(t, jsonMarshalerType) || implements(t, textMarshalerType) {
		return nil, fmt.Errorf("redact: %s: Verbatim type %s must not marshal itself", path, t)
	}
	if implements(t, enumType) || implements(t, durationIface) {
		return nil, fmt.Errorf("redact: %s: Verbatim type %s is also an Enum or Duration", path, t)
	}
	pat := reflect.New(t).Interface().(Verbatim).VerbatimPattern()
	re, err := syntax.Parse(pat, syntax.Perl)
	if err != nil {
		return nil, fmt.Errorf("redact: %s: Verbatim type %s: invalid pattern: %w", path, t, err)
	}
	maxLen, err := verbatimMaxLen(re)
	if err != nil {
		return nil, fmt.Errorf("redact: %s: Verbatim type %s: %w", path, t, err)
	}
	if maxLen > maxVerbatimLen {
		return nil, fmt.Errorf("redact: %s: Verbatim type %s matches up to %d bytes (max %d)", path, t, maxLen, maxVerbatimLen)
	}
	full := regexp.MustCompile(`\A(?:` + pat + `)\z`) // pat parsed above, so this compiles
	if full.MatchString("") {
		return nil, fmt.Errorf("redact: %s: Verbatim type %s matches the empty string", path, t)
	}
	return full, nil
}

var errUnboundedPattern = errors.New("pattern has unbounded repetition (* or + or {n,})")

// verbatimMaxLen returns the longest value re can match, in bytes (every allowed rune is one
// byte), or an error for a construct a Verbatim pattern may not use.
func verbatimMaxLen(re *syntax.Regexp) (int, error) {
	switch re.Op {
	case syntax.OpEmptyMatch:
		return 0, nil
	case syntax.OpLiteral:
		if re.Flags&syntax.FoldCase != 0 {
			return 0, errors.New("pattern uses case folding, which reaches non-ASCII runes")
		}
		for _, r := range re.Rune {
			if !verbatimRune(r) {
				return 0, fmt.Errorf("pattern literal has rune %U outside printable ASCII", r)
			}
		}
		return len(re.Rune), nil
	case syntax.OpCharClass:
		for i := 0; i < len(re.Rune); i += 2 {
			if !verbatimRune(re.Rune[i]) || !verbatimRune(re.Rune[i+1]) {
				return 0, fmt.Errorf("pattern class has runes %U-%U outside printable ASCII", re.Rune[i], re.Rune[i+1])
			}
		}
		if len(re.Rune) == 0 {
			return 0, errors.New("pattern has an empty class")
		}
		return 1, nil
	case syntax.OpCapture:
		return verbatimMaxLen(re.Sub[0])
	case syntax.OpQuest:
		return verbatimMaxLen(re.Sub[0])
	case syntax.OpRepeat:
		if re.Max < 0 {
			return 0, errUnboundedPattern
		}
		n, err := verbatimMaxLen(re.Sub[0])
		return n * re.Max, err
	case syntax.OpConcat:
		total := 0
		for _, sub := range re.Sub {
			n, err := verbatimMaxLen(sub)
			if err != nil {
				return 0, err
			}
			total += n
		}
		return total, nil
	case syntax.OpAlternate:
		longest := 0
		for _, sub := range re.Sub {
			n, err := verbatimMaxLen(sub)
			if err != nil {
				return 0, err
			}
			longest = max(longest, n)
		}
		return longest, nil
	case syntax.OpStar, syntax.OpPlus:
		return 0, errUnboundedPattern
	default:
		return 0, fmt.Errorf("pattern uses %s, which a Verbatim pattern may not", re.Op)
	}
}

// verbatimRune reports whether r is printable ASCII other than the space.
func verbatimRune(r rune) bool {
	return r >= '!' && r <= '~'
}
