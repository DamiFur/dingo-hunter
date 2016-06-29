package cfsmextract

import (
	"fmt"
	"go/types"
	"os"

	"github.com/nickng/dingo-hunter/cfsmextract/sesstype"
	"github.com/nickng/dingo-hunter/cfsmextract/utils"
	"golang.org/x/tools/go/ssa"
)

// VarKind specifies the type of utils.VarDef used in frame.local[]
type VarKind int

// VarKind definitions
const (
	Nothing     VarKind = iota // Not in frame.local[]
	Array                      // Array in heap
	Chan                       // Channel
	Struct                     // Struct in heap
	LocalArray                 // Array in frame
	LocalStruct                // Struct in frame
	Untracked                  // Not a tracked VarKind but in frame.local[]
)

// Captures are lists of VarDefs for closure captures
type Captures []*utils.Definition

// Tuples are lists of VarDefs from multiple return values
type Tuples []*utils.Definition

// Elems are maps from array indices (variable) to VarDefs
type Elems map[ssa.Value]*utils.Definition

// Fields are maps from struct fields (integer) to VarDefs
type Fields map[int]*utils.Definition

// Frame holds variables in current function scope
type frame struct {
	fn      *ssa.Function                   // Function ptr of callee
	locals  map[ssa.Value]*utils.Definition // Holds definitions of local registers
	arrays  map[*utils.Definition]Elems     // Array elements (Alloc local)
	structs map[*utils.Definition]Fields    // Struct fields (Alloc local)
	tuples  map[ssa.Value]Tuples            // Multiple return values as tuple
	phi     map[ssa.Value][]ssa.Value       // Phis
	recvok  map[ssa.Value]*sesstype.Chan    // Channel used in recvok
	retvals Tuples                          // Return values to pass back to parent
	defers  []*ssa.Defer                    // Deferred calls
	caller  *frame                          // Ptr to caller's frame, nil if main/ext
	env     *environ                        // Environment
	gortn   *goroutine                      // Current goroutine
}

// Environment: Variables/info available globally for all goroutines
type environ struct {
	session  *sesstype.Session
	extract  *CFSMExtract
	globals  map[ssa.Value]*utils.Definition      // Globals
	arrays   map[*utils.Definition]Elems          // Array elements
	structs  map[*utils.Definition]Fields         // Struct fields
	chans    map[*utils.Definition]*sesstype.Chan // Channels
	extern   map[ssa.Value]types.Type             // Values that originates externally, we are only sure of its type
	closures map[ssa.Value]Captures               // Closure captures
	selNode  map[ssa.Value]struct {               // Parent nodes of select
		parent   *sesstype.Node
		blocking bool
	}
	selIdx  map[ssa.Value]ssa.Value // Mapping from select index to select SSA Value
	selTest map[ssa.Value]struct {  // Records test for select-branch index
		idx int       // The index of the branch
		tpl ssa.Value // The SelectState tuple which the branch originates from
	}
	recvTest map[ssa.Value]*sesstype.Chan // Receive test
	ifparent *sesstype.NodeStack
}

func (env *environ) GetSessionChan(vd *utils.Definition) *sesstype.Chan {
	if ch, ok := env.session.Chans[vd]; ok {
		return &ch
	}
	panic(fmt.Sprintf("Channel %s undefined in session", vd.String()))
}

func makeToplevelFrame(extract *CFSMExtract) *frame {
	callee := &frame{
		fn:      nil,
		locals:  make(map[ssa.Value]*utils.Definition),
		arrays:  make(map[*utils.Definition]Elems),
		structs: make(map[*utils.Definition]Fields),
		tuples:  make(map[ssa.Value]Tuples),
		phi:     make(map[ssa.Value][]ssa.Value),
		recvok:  make(map[ssa.Value]*sesstype.Chan),
		retvals: make(Tuples, 0),
		defers:  make([]*ssa.Defer, 0),
		caller:  nil,
		env: &environ{
			session:  extract.session,
			extract:  extract,
			globals:  make(map[ssa.Value]*utils.Definition),
			arrays:   make(map[*utils.Definition]Elems),
			structs:  make(map[*utils.Definition]Fields),
			chans:    make(map[*utils.Definition]*sesstype.Chan),
			extern:   make(map[ssa.Value]types.Type),
			closures: make(map[ssa.Value]Captures),
			selNode: make(map[ssa.Value]struct {
				parent   *sesstype.Node
				blocking bool
			}),
			selIdx: make(map[ssa.Value]ssa.Value),
			selTest: make(map[ssa.Value]struct {
				idx int
				tpl ssa.Value
			}),
			recvTest: make(map[ssa.Value]*sesstype.Chan),
			ifparent: sesstype.NewNodeStack(),
		},
		gortn: &goroutine{
			role:    extract.session.GetRole("main"),
			root:    sesstype.NewLabelNode("main"),
			leaf:    nil,
			visited: make(map[*ssa.BasicBlock]sesstype.Node),
		},
	}
	callee.gortn.leaf = &callee.gortn.root

	return callee
}

func (caller *frame) callBuiltin(common *ssa.CallCommon) {
	builtin := common.Value.(*ssa.Builtin)
	if builtin.Name() == "close" {
		if len(common.Args) == 1 {
			if ch, ok := caller.env.chans[caller.locals[common.Args[0]]]; ok {
				fmt.Fprintf(os.Stderr, "++ call builtin %s(%s channel %s)\n", orange(builtin.Name()), green(common.Args[0].Name()), ch.Name())
				visitClose(*ch, caller)
			} else {
				panic("Builtin close() called with non-channel\n")
			}
		}
	} else if builtin.Name() == "copy" {
		dst := common.Args[0]
		src := common.Args[1]
		fmt.Fprintf(os.Stderr, "++ call builtin %s(%s <- %s)\n", orange("copy"), dst.Name(), src.Name())
		caller.locals[dst] = caller.locals[src]
		return
	} else {
		fmt.Fprintf(os.Stderr, "++ call builtin %s(", builtin.Name())
		for _, arg := range common.Args {
			fmt.Fprintf(os.Stderr, "%s", arg.Name())
		}
		fmt.Fprintf(os.Stderr, ") # TODO (handle builtin)\n")
	}
}

func (caller *frame) call(c *ssa.Call) {
	caller.callCommon(c, c.Common())
}

func (caller *frame) callCommon(call *ssa.Call, common *ssa.CallCommon) {
	switch fn := common.Value.(type) {
	case *ssa.Builtin:
		caller.callBuiltin(common)

	case *ssa.MakeClosure:
		// TODO(nickng) Handle calling closure
		fmt.Fprintf(os.Stderr, "   # TODO (handle closure) %s\n", fn.String())

	case *ssa.Function:
		if common.StaticCallee() == nil {
			panic("Call with nil CallCommon!")
		}

		callee := &frame{
			fn:      common.StaticCallee(),
			locals:  make(map[ssa.Value]*utils.Definition),
			arrays:  make(map[*utils.Definition]Elems),
			structs: make(map[*utils.Definition]Fields),
			tuples:  make(map[ssa.Value]Tuples),
			phi:     make(map[ssa.Value][]ssa.Value),
			recvok:  make(map[ssa.Value]*sesstype.Chan),
			retvals: make(Tuples, common.Signature().Results().Len()),
			defers:  make([]*ssa.Defer, 0),
			caller:  caller,
			env:     caller.env,   // Use the same env as caller
			gortn:   caller.gortn, // Use the same role as caller
		}

		fmt.Fprintf(os.Stderr, "++ call %s(", orange(common.StaticCallee().String()))
		callee.translate(common)
		fmt.Fprintf(os.Stderr, ")\n")

		if callee.isRecursive() {
			fmt.Fprintf(os.Stderr, "-- Recursive %s()\n", orange(common.StaticCallee().String()))
			callee.printCallStack()
		} else {
			if hasCode := visitFunc(callee.fn, callee); hasCode {
				caller.handleRetvals(call.Value(), callee)
			} else {
				caller.handleExtRetvals(call.Value(), callee)
			}
			fmt.Fprintf(os.Stderr, "-- return from %s (%d retvals)\n", orange(common.StaticCallee().String()), len(callee.retvals))
		}

	default:
		if !common.IsInvoke() {
			fmt.Fprintf(os.Stderr, "Unknown call type %v\n", common)
			return
		}

		switch vd, kind := caller.get(common.Value); kind {
		case Struct, LocalStruct:
			fmt.Fprintf(os.Stderr, "++ invoke %s.%s, type=%s\n", reg(common.Value), common.Method.String(), vd.Var.Type().String())
			// If dealing with interfaces, check that the method is invokable
			if iface, ok := common.Value.Type().Underlying().(*types.Interface); ok {
				if meth, _ := types.MissingMethod(vd.Var.Type(), iface, true); meth != nil {
					fmt.Fprintf(os.Stderr, "     ^ interface not fully implemented\n")
				} else {
					fn := findMethod(common.Value.Parent().Prog, common.Method, vd.Var.Type())
					if fn != nil {
						fmt.Fprintf(os.Stderr, "     ^ found function %s\n", fn.String())

						callee := &frame{
							fn:      fn,
							locals:  make(map[ssa.Value]*utils.Definition),
							arrays:  make(map[*utils.Definition]Elems),
							structs: make(map[*utils.Definition]Fields),
							tuples:  make(map[ssa.Value]Tuples),
							phi:     make(map[ssa.Value][]ssa.Value),
							recvok:  make(map[ssa.Value]*sesstype.Chan),
							retvals: make(Tuples, common.Signature().Results().Len()),
							defers:  make([]*ssa.Defer, 0),
							caller:  caller,
							env:     caller.env,   // Use the same env as caller
							gortn:   caller.gortn, // Use the same role as caller
						}

						common.Args = append([]ssa.Value{common.Value}, common.Args...)
						fmt.Fprintf(os.Stderr, "++ call %s(", orange(fn.String()))
						callee.translate(common)
						fmt.Fprintf(os.Stderr, ")\n")

						if callee.isRecursive() {
							fmt.Fprintf(os.Stderr, "-- Recursive %s()\n", orange(fn.String()))
							callee.printCallStack()
						} else {
							if hasCode := visitFunc(callee.fn, callee); hasCode {
								caller.handleRetvals(call.Value(), callee)
							} else {
								caller.handleExtRetvals(call.Value(), callee)
							}
							fmt.Fprintf(os.Stderr, "-- return from %s (%d retvals)\n", orange(fn.String()), len(callee.retvals))
						}

					} else {
						panic(fmt.Sprintf("Cannot call function: %s.%s is abstract (program not well-formed)", common.Value, common.Method.String()))
					}
				}
			} else {
				fmt.Fprintf(os.Stderr, "     ^ method %s.%s does not exist\n", reg(common.Value), common.Method.String())
			}

		default:
			fmt.Fprintf(os.Stderr, "++ invoke %s.%s\n", reg(common.Value), common.Method.String())
		}
	}
}

func findMethod(prog *ssa.Program, meth *types.Func, typ types.Type) *ssa.Function {
	if meth != nil {
		fmt.Fprintf(os.Stderr, "     ^ finding method for type: %s pkg: %s name: %s\n", typ.String(), meth.Pkg().Name(), meth.Name())
	}
	return prog.LookupMethod(typ, meth.Pkg(), meth.Name())
}

func (caller *frame) callGo(g *ssa.Go) {
	common := g.Common()
	goname := fmt.Sprintf("%s_%d", common.Value.Name(), int(g.Pos()))
	gorole := caller.env.session.GetRole(goname)

	callee := &frame{
		fn:      common.StaticCallee(),
		locals:  make(map[ssa.Value]*utils.Definition),
		arrays:  make(map[*utils.Definition]Elems),
		structs: make(map[*utils.Definition]Fields),
		tuples:  make(map[ssa.Value]Tuples),
		phi:     make(map[ssa.Value][]ssa.Value),
		recvok:  make(map[ssa.Value]*sesstype.Chan),
		retvals: make(Tuples, common.Signature().Results().Len()),
		defers:  make([]*ssa.Defer, 0),
		caller:  caller,
		env:     caller.env, // Use the same env as caller
		gortn: &goroutine{
			role:    gorole,
			root:    sesstype.NewLabelNode(goname),
			leaf:    nil,
			visited: make(map[*ssa.BasicBlock]sesstype.Node),
		},
	}
	callee.gortn.leaf = &callee.gortn.root

	fmt.Fprintf(os.Stderr, "@@ queue go %s(", common.StaticCallee().String())
	callee.translate(common)
	fmt.Fprintf(os.Stderr, ")\n")

	// TODO(nickng) Does not stop at recursive call.
	caller.env.extract.goQueue = append(caller.env.extract.goQueue, callee)
}

func (callee *frame) translate(common *ssa.CallCommon) {
	for i, param := range callee.fn.Params {
		argParent := common.Args[i]
		if param != argParent {
			if vd, ok := callee.caller.locals[argParent]; ok {
				callee.locals[param] = vd
			}
		}

		if i > 0 {
			fmt.Fprintf(os.Stderr, ", ")
		}

		fmt.Fprintf(os.Stderr, "%s:caller[%s] = %s", orange(param.Name()), reg(common.Args[i]), callee.locals[param].String())
		myVD := callee.locals[param] // VD of parameter (which are in callee.locals)

		// if argument is a channel
		if ch, ok := callee.env.chans[myVD]; ok {
			fmt.Fprintf(os.Stderr, " channel %s", (*ch).Name())
		} else if _, ok := callee.env.structs[myVD]; ok {
			fmt.Fprintf(os.Stderr, " struct")
		} else if _, ok := callee.env.arrays[myVD]; ok {
			fmt.Fprintf(os.Stderr, " array")
		} else if fields, ok := callee.caller.structs[myVD]; ok {
			// If param is local struct in caller, make local copy
			fmt.Fprintf(os.Stderr, " lstruct")
			callee.structs[myVD] = fields
		} else if elems, ok := callee.caller.arrays[myVD]; ok {
			// If param is local array in caller, make local copy
			fmt.Fprintf(os.Stderr, " larray")
			callee.arrays[myVD] = elems
		}
	}

	// Closure capture (copy from env.closures assigned in MakeClosure).
	if captures, isClosure := callee.env.closures[common.Value]; isClosure {
		for idx, fv := range callee.fn.FreeVars {
			callee.locals[fv] = captures[idx]
			fmt.Fprintf(os.Stderr, ", capture %s = %s", fv.Name(), captures[idx].String())
		}
	}
}

// handleRetvals looks up and stores return value from function calls.
// Nothing will be done if there are no return values from the function.
func (caller *frame) handleRetvals(returned ssa.Value, callee *frame) {
	if len(callee.retvals) > 0 {
		if len(callee.retvals) == 1 {
			// Single return value (callee.retvals[0])
			caller.locals[returned] = callee.retvals[0]
		} else {
			// Multiple return values (callee.retvals tuple)
			caller.tuples[returned] = callee.retvals
		}
	}
}

func (callee *frame) get(v ssa.Value) (*utils.Definition, VarKind) {
	if vd, ok := callee.locals[v]; ok {
		if _, ok := callee.env.arrays[vd]; ok {
			return vd, Array
		}
		if _, ok := callee.arrays[vd]; ok {
			return vd, LocalArray
		}
		if _, ok := callee.env.chans[vd]; ok {
			return vd, Chan
		}
		if _, ok := callee.env.structs[vd]; ok {
			return vd, Struct
		}
		if _, ok := callee.structs[vd]; ok {
			return vd, LocalStruct
		}
		return vd, Untracked
	} else if vs, ok := callee.phi[v]; ok {
		for i := len(vs) - 1; i >= 0; i-- {
			if chVd, defined := callee.locals[vs[i]]; defined {
				return chVd, Chan
			}
		}
	}
	return nil, Nothing
}

// handleExtRetvals looks up and stores return value from (ext) function calls.
// Ext functions have no code (no body to analyse) and unlike normal values,
// the return values/tuples are stored until they are referenced.
func (caller *frame) handleExtRetvals(returned ssa.Value, callee *frame) {
	// Since there are no code for the function, we use the function
	// signature to see if any of these are channels.
	// XXX We don't know where these come from so we put them in extern.
	resultsLen := callee.fn.Signature.Results().Len()
	if resultsLen > 0 {
		caller.env.extern[returned] = callee.fn.Signature.Results()
		if resultsLen == 1 {
			fmt.Fprintf(os.Stderr, "-- Return from %s (builtin/ext) with a single value\n", callee.fn.String())
			if _, ok := callee.fn.Signature.Results().At(0).Type().(*types.Chan); ok {
				vardef := utils.NewDef(returned)
				ch := caller.env.session.MakeExtChan(vardef, caller.gortn.role)
				caller.env.chans[vardef] = &ch
				fmt.Fprintf(os.Stderr, "-- Return value from %s (builtin/ext) is a channel %s (ext)\n", callee.fn.String(), (*caller.env.chans[vardef]).Name())
			}
		} else {
			fmt.Fprintf(os.Stderr, "-- Return from %s (builtin/ext) with %d-tuple\n", callee.fn.String(), resultsLen)
		}
	}
}

func (callee *frame) isRecursive() bool {
	var tracebackFns []*ssa.Function
	foundFr := callee
	for fr := callee.caller; fr != nil; fr = fr.caller {
		tracebackFns = append(tracebackFns, fr.fn)
		if fr.fn == callee.fn {
			foundFr = fr
			break
		}
	}
	// If same function is not found, not recursive
	if foundFr == callee {
		return false
	}

	// Otherwise try to trace back with foundFr and is recursive if all matches
	for _, fn := range tracebackFns {
		if foundFr == nil || foundFr.fn != fn {
			return false
		}
		foundFr = foundFr.caller
	}
	return true
}

func (callee *frame) printCallStack() {
	curFr := callee
	for curFr != nil && curFr.fn != nil {
		fmt.Fprintf(os.Stderr, "Called by: %s()\n", curFr.fn.String())
		curFr = curFr.caller
	}
}

func (callee *frame) updateDefs(vdOld, vdNew *utils.Definition) {
	for def, array := range callee.arrays {
		for k, v := range array {
			if v == vdOld {
				callee.arrays[def][k] = vdNew
			}
		}
	}
	for def, array := range callee.env.arrays {
		for k, v := range array {
			if v == vdOld {
				callee.env.arrays[def][k] = vdNew
			}
		}
	}
	for def, struc := range callee.structs {
		for i, field := range struc {
			if field == vdOld {
				callee.structs[def][i] = vdNew
			}
		}
	}
	for def, struc := range callee.env.structs {
		for i, field := range struc {
			if field == vdOld {
				callee.env.structs[def][i] = vdNew
			}
		}
	}
}
