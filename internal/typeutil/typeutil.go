// Package typeutil has the go/types helpers shared by the compiler, the
// layer generator and the analyzers.
package typeutil

import "go/types"

var errorType = types.Universe.Lookup("error").Type()

// IsError reports whether t is the error type.
func IsError(t types.Type) bool { return t != nil && types.Identical(t, errorType) }

// ImplementsError reports whether t implements error.
func ImplementsError(t types.Type) bool {
	return t != nil && types.Implements(t, errorType.Underlying().(*types.Interface))
}

// IsNamed reports whether t is the named type pkg.name.
func IsNamed(t types.Type, pkg, name string) bool {
	if t == nil {
		return false
	}
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkg && n.Obj().Name() == name
}

// IsContext reports whether t is context.Context.
func IsContext(t types.Type) bool { return IsNamed(t, "context", "Context") }

// Zero returns the zero value of t as Go source; typeString renders types.
func Zero(t types.Type, typeString func(types.Type) string) string {
	if t == nil {
		return "nil"
	}
	if _, ok := t.(*types.TypeParam); ok {
		return "*new(" + typeString(t) + ")"
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return "false"
		case u.Info()&types.IsString != 0:
			return `""`
		case u.Info()&types.IsNumeric != 0:
			return "0"
		}
	case *types.Struct, *types.Array:
		return typeString(t) + "{}"
	}
	return "nil"
}
