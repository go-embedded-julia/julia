package lower

import (
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
)

// The default constructors of a struct are not in the lowered struct
// definition: the runtime builds them when Core._defaultctors runs
// (jl_ctor_def, method.c:1327-1454), asking the front end for their bodies
// through jl_outer_ctor_body and jl_inner_ctor_body (ast.c:1372-1398).

// DefaultInnerCtorBody is jl-default-inner-ctor-body (jlfrontend.scm:201-202
// over julia-syntax.scm:766-777): the lowered body of a default inner
// constructor, a *expr.CodeInfo. fieldKinds has one entry per field: -1 for
// no conversion, 0 for a conversion to the field type, n > 0 for one to
// static parameter n.
func (st *State) DefaultInnerCtorBody(fieldKinds []int64, file string, line int64) (any, error) {
	return st.lowerBuilt(func(lw *lowerer) any {
		name := sym("#ctor-self#")
		fieldNames := ctorFieldNames(len(fieldKinds))
		vals := make([]any, len(fieldKinds))
		for i, k := range fieldKinds {
			val := fieldNames[i]
			switch {
			case k == -1:
				vals[i] = val
			case k > 0:
				vals[i] = lw.convertForTypeDecl(val, list(sym("static_parameter"), k), false, nil)
			default:
				vals[i] = lw.convertForTypeDecl(val, list(sym("call"), coreRef("fieldtype"), name, int64(i+1)), false, nil)
			}
		}
		return ctorLambda(name, fieldNames, append([]any{sym("new"), name}, vals...), file, line)
	}, file, line)
}

// DefaultOuterCtorBody is jl-default-outer-ctor-body (jlfrontend.scm:204-205
// over julia-syntax.scm:780-788): the lowered body of a default outer
// constructor of thistype, with nfields arguments and nsparams static
// parameters.
func (st *State) DefaultOuterCtorBody(thistype any, nfields, nsparams int, file string, line int64) (any, error) {
	return st.lowerBuilt(func(lw *lowerer) any {
		name := sym("#ctor-self#")
		fieldNames := ctorFieldNames(nfields)
		var typ any = name
		if nsparams > 0 {
			curly := []any{sym("curly"), &jlValue{thistype}}
			for i := 0; i < nsparams; i++ {
				curly = append(curly, list(sym("static_parameter"), int64(i+1)))
			}
			typ = curly
		}
		return ctorLambda(name, fieldNames, append([]any{sym("new"), typ}, fieldNames...), file, line)
	}, file, line)
}

func ctorFieldNames(n int) []any {
	names := make([]any, n)
	for i := range names {
		names[i] = sym(fmt.Sprintf("_%d", i+1))
	}
	return names
}

// ctorLambda is `(lambda (,name ,@fields) () (scope-block (block (line …)
// (return ,newExpr))))`.
func ctorLambda(name sym, fieldNames []any, newExpr []any, file string, line int64) any {
	body := list(sBlock, list(sLine, line, sym(file)), list(sReturn, newExpr))
	return list(sLambda, append([]any{name}, fieldNames...), []any{}, list(sym("scope-block"), body))
}

// lowerBuilt is expand-to-thunk- (jlfrontend.scm:166-168) of a form the
// front end builds itself, converted back to julia values as
// jl_call_scm_on_ast_and_loc does.
func (st *State) lowerBuilt(build func(lw *lowerer) any, file string, line int64) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%w: %v", ErrInternal, r)
		}
	}()
	lw := &lowerer{st: st}
	res := lw.errorWrap(func() any { return lw.expandToplevelExpr(build(lw), sym(file), line) })
	c := &converter{mod: st.mod}
	v := c.fromSx(res)
	if _, ok := v.(*expr.CodeInfo); !ok {
		return nil, fmt.Errorf("%w: a constructor body lowered to %T", ErrInternal, v)
	}
	return v, nil
}
