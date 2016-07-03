package typeinfer

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"

	"github.com/nickng/dingo-hunter/typeinfer/migo"
	"golang.org/x/tools/go/ssa"
)

// visitFunc analyses function body.
func visitFunc(fn *ssa.Function, infer *TypeInfer, f *Function) {
	infer.Env.MigoProg.AddFunction(f.FuncDef)

	infer.Logger.Printf(f.Sprintf(FuncEnterSymbol+"───── func %s ─────", fn.Name()))
	defer infer.Logger.Printf(f.Sprintf(FuncExitSymbol+"───── func %s ─────", fn.Name()))
	if fn.Name() == "init" {
		if _, ok := f.Prog.InitPkgs[fn.Package()]; !ok {
			f.Prog.InitPkgs[fn.Package()] = true
		}
		f.hasBody = true
		return
	}
	for val, instance := range f.locals {
		infer.Logger.Printf(f.Sprintf(ParamSymbol+"%s = %s", val.Name(), instance))
	}

	if fn.Blocks == nil {
		infer.Logger.Print(f.Sprintf(MoreSymbol + "« no function body »"))
		f.hasBody = false // No body
		return
	}
	visitBasicBlock(fn.Blocks[0], infer, f, NewBlock(f, fn.Blocks[0], 0), &Loop{Parent: f})
	f.hasBody = true
}

func visitBasicBlock(blk *ssa.BasicBlock, infer *TypeInfer, f *Function, prevB *Block, l *Loop) {
	loopStateTransition(blk, infer, f, &l)
	if l.Bound == Static && l.HasNext() {
		infer.Logger.Printf(f.Sprintf(BlockSymbol+"%s %d (loop %s=%d)", fmtBlock("block"), blk.Index, l.CondVar.Name(), l.Index))
		// Loop and can continue, so don't mark as visited yet
	} else {
		if _, ok := f.Visited[blk]; ok {
			infer.Logger.Printf(f.Sprintf(BlockSymbol+"%s %d (visited)", fmtBlock("block"), blk.Index))
			f.Visited[blk]++
			return
		}
	}
	infer.Logger.Printf(f.Sprintf(BlockSymbol+"%s %d; %s", fmtBlock("block"), blk.Index, fmtLoopHL(blk.Comment)))
	f.Visited[blk] = 0
	for _, instr := range blk.Instrs {
		visitInstr(instr, infer, f, prevB, l)
	}
}

func visitInstr(instr ssa.Instruction, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	switch instr := instr.(type) {
	case *ssa.Alloc:
		visitAlloc(instr, infer, f, b, l)
	case *ssa.BinOp:
		visitBinOp(instr, infer, f, b, l)
	case *ssa.Call:
		visitCall(instr, infer, f, b, l)
	case *ssa.ChangeInterface:
		visitChangeInterface(instr, infer, f, b, l)
	case *ssa.ChangeType:
		visitChangeType(instr, infer, f, b, l)
	case *ssa.Convert:
		visitConvert(instr, infer, f, b, l)
	case *ssa.DebugRef:
		//infer.Logger.Printf(f.Sprintf(SkipSymbol+"debug\t\t%s", instr))
	case *ssa.Defer:
		visitDefer(instr, infer, f, b, l)
	case *ssa.Extract:
		visitExtract(instr, infer, f, b, l)
	case *ssa.FieldAddr:
		visitFieldAddr(instr, infer, f, b, l)
	case *ssa.Go:
		visitGo(instr, infer, f, b, l)
	case *ssa.If:
		visitIf(instr, infer, f, b, l)
	case *ssa.Index:
		visitIndex(instr, infer, f, b, l)
	case *ssa.IndexAddr:
		visitIndexAddr(instr, infer, f, b, l)
	case *ssa.Jump:
		visitJump(instr, infer, f, b, l)
	case *ssa.Lookup:
		visitLookup(instr, infer, f, b, l)
	case *ssa.MakeChan:
		visitMakeChan(instr, infer, f, b, l)
	case *ssa.MakeClosure:
		visitMakeClosure(instr, infer, f, b, l)
	case *ssa.MakeInterface:
		visitMakeInterface(instr, infer, f, b, l)
	case *ssa.MakeMap:
		visitMakeMap(instr, infer, f, b, l)
	case *ssa.MakeSlice:
		visitMakeSlice(instr, infer, f, b, l)
	case *ssa.MapUpdate:
		visitMapUpdate(instr, infer, f, b, l)
	case *ssa.Next:
		visitNext(instr, infer, f, b, l)
	case *ssa.Phi:
		visitPhi(instr, infer, f, b, l)
	case *ssa.Return:
		visitReturn(instr, infer, f, b, l)
	case *ssa.RunDefers:
		visitRunDefers(instr, infer, f, b, l)
	case *ssa.Send:
		visitSend(instr, infer, f, b, l)
	case *ssa.Select:
		visitSelect(instr, infer, f, b, l)
	case *ssa.Slice:
		visitSlice(instr, infer, f, b, l)
	case *ssa.Store:
		visitStore(instr, infer, f, b, l)
	case *ssa.TypeAssert:
		visitTypeAssert(instr, infer, f, b, l)
	case *ssa.UnOp:
		switch instr.Op {
		case token.ARROW:
			visitRecv(instr, infer, f, b, l)
		case token.MUL:
			visitDeref(instr, infer, f, b, l)
		default:
			visitSkip(instr, infer, f, b, l)
		}
	default:
		visitSkip(instr, infer, f, b, l)
	}
}

func visitAlloc(instr *ssa.Alloc, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	allocType := instr.Type().(*types.Pointer).Elem()
	switch t := allocType.Underlying().(type) {
	case *types.Array: // Static size array
		f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
		if instr.Heap {
			f.Prog.arrays[f.locals[instr]] = make(Elems, t.Len())
			infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc (array@heap) of type %s (%d elems)", f.locals[instr], instr.Type(), t.Len()))
		} else {
			f.arrays[f.locals[instr]] = make(Elems, t.Len())
			infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc (array@local) of type %s (%d elems)", f.locals[instr], instr.Type(), t.Len()))
		}
	case *types.Struct:
		f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
		if instr.Heap {
			f.Prog.structs[f.locals[instr]] = make(Fields, t.NumFields())
			infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc (struct@heap) of type %s (%d fields)", f.locals[instr], instr.Type(), t.NumFields()))
		} else {
			f.structs[f.locals[instr]] = make(Fields, t.NumFields())
			infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc (struct@local) of type %s (%d fields)", f.locals[instr], instr.Type(), t.NumFields()))
		}
	case *types.Pointer:
		switch pt := t.Elem().Underlying().(type) {
		case *types.Array:
			if instr.Heap {
				f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
				f.Prog.arrays[f.locals[instr]] = make(Elems, pt.Len())
				infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc/indirect (array@heap) of type %s (%d elems)", f.locals[instr], instr.Type(), pt.Len()))
			} else {
				f.arrays[f.locals[instr]] = make(Elems, pt.Len())
				infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc/indirect (array@local) of type %s (%d elems)", f.locals[instr], instr.Type(), pt.Len()))
			}
		case *types.Struct:
			f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
			if instr.Heap {
				f.Prog.structs[f.locals[instr]] = make(Fields, pt.NumFields())
				infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc/indirect (struct@heap) of type %s (%d fields)", f.locals[instr], instr.Type(), pt.NumFields()))
			} else {
				f.structs[f.locals[instr]] = make(Fields, pt.NumFields())
				infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc/indirect (struct@local) of type %s (%d fields)", f.locals[instr], instr.Type(), pt.NumFields()))
			}
		default:
			f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
			infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc/indirect of type %s", f.locals[instr], instr.Type().Underlying()))
		}
	default:
		f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
		infer.Logger.Print(f.Sprintf(NewSymbol+"%s = alloc of type %s", f.locals[instr], instr.Type().Underlying()))
	}
}

func visitBinOp(instr *ssa.BinOp, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	if l.State == Enter {
		switch l.Bound {
		case Unknown:
			switch instr.Op {
			case token.LSS: // i < N
				if i, ok := instr.Y.(*ssa.Const); ok && i.Value.Kind() == constant.Int {
					l.SetCond(instr, i.Int64()-1)
					if _, ok := instr.X.(*ssa.Phi); ok && l.Start < l.End {
						l.Bound = Static
						infer.Logger.Printf(f.Sprintf(LoopSymbol+"i <= %s", fmtLoopHL(l.End)))
						return
					}
					l.Bound = Dynamic
					return
				}
			case token.LEQ: // i <= N
				if i, ok := instr.Y.(*ssa.Const); ok && i.Value.Kind() == constant.Int {
					l.SetCond(instr, i.Int64())
					if _, ok := instr.X.(*ssa.Phi); ok && l.Start < l.End {
						l.Bound = Static
						infer.Logger.Printf(f.Sprintf(LoopSymbol+"i <= %s", fmtLoopHL(l.End)))
						return
					}
					l.Bound = Dynamic
					return
				}
			case token.GTR: // i > N
				if i, ok := instr.Y.(*ssa.Const); ok && i.Value.Kind() == constant.Int {
					l.SetCond(instr, i.Int64()+1)
					if _, ok := instr.X.(*ssa.Phi); ok && l.Start > l.End {
						l.Bound = Static
						infer.Logger.Printf(f.Sprintf(LoopSymbol+"i > %s", fmtLoopHL(l.End)))
						return
					}
					l.Bound = Dynamic
					return
				}
			case token.GEQ: // i >= N
				if i, ok := instr.Y.(*ssa.Const); ok && i.Value.Kind() == constant.Int {
					l.SetCond(instr, i.Int64())
					if _, ok := instr.X.(*ssa.Phi); ok && l.Start > l.End {
						l.Bound = Static
						infer.Logger.Printf(f.Sprintf(LoopSymbol+"i >= %s", fmtLoopHL(l.End)))
						return
					}
					l.Bound = Dynamic
					return
				}
			}
		}
	}
	f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
	visitSkip(instr, infer, f, b, l)
}

func visitCall(instr *ssa.Call, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	infer.Logger.Printf(f.Sprintf(CallSymbol+"%s = %s", instr.Name(), instr.String()))
	f.Call(instr, infer, b, l)
}

func visitChangeType(instr *ssa.ChangeType, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	inst, ok := f.locals[instr.X]
	if !ok {
		infer.Logger.Fatalf("changetype: %s: %v → %v", ErrUnknownValue, instr.X, instr)
		return
	}
	f.locals[instr] = inst
	if a, ok := f.arrays[f.locals[instr.X]]; ok {
		f.arrays[f.locals[instr]] = a
	}
	if s, ok := f.structs[f.locals[instr.X]]; ok {
		f.structs[f.locals[instr]] = s
	}
	if m, ok := f.maps[f.locals[instr.X]]; ok {
		f.maps[f.locals[instr]] = m
	}
	infer.Logger.Print(f.Sprintf(ValSymbol+"%s = %s (type: %s ← %s)", instr.Name(), instr.X.Name(), fmtType(instr.Type()), fmtType(instr.X.Type().Underlying())))
	return
}

func visitChangeInterface(instr *ssa.ChangeInterface, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	inst, ok := f.locals[instr.X]
	if !ok {
		infer.Logger.Fatalf("changeiface: %s: %v → %v", ErrUnknownValue, instr.X, instr)
	}
	f.locals[instr] = inst
}

func visitConvert(instr *ssa.Convert, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	_, ok := f.locals[instr.X]
	if !ok {
		if c, ok := instr.X.(*ssa.Const); ok {
			f.locals[instr.X] = &ConstInstance{c}
		} else if _, ok := instr.X.(*ssa.Global); ok {
			inst, ok := f.Prog.globals[instr.X]
			if !ok {
				infer.Logger.Fatalf("convert (global): %s: %+v", ErrUnknownValue, instr.X)
			}
			f.locals[instr.X] = inst
			infer.Logger.Print(f.Sprintf(SkipSymbol+"%s convert= %s (global)", f.locals[instr], instr.X.Name()))
			return
		} else {
			infer.Logger.Fatalf("convert: %s: %+v", ErrUnknownValue, instr.X)
			return
		}
	}
	f.locals[instr] = f.locals[instr.X]
	infer.Logger.Print(f.Sprintf(SkipSymbol+"%s convert= %s", f.locals[instr], instr.X.Name()))
}

func visitDefer(instr *ssa.Defer, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	f.defers = append(f.defers, instr)
}

func visitDeref(instr *ssa.UnOp, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	ptr, val := instr.X, instr
	// Global.
	if _, ok := ptr.(*ssa.Global); ok {
		inst, ok := f.Prog.globals[ptr]
		if !ok {
			infer.Logger.Fatalf("deref (global): %s: %+v", ErrUnknownValue, ptr)
			return
		}
		f.locals[ptr], f.locals[val] = inst, inst
		infer.Logger.Print(f.Sprintf(ValSymbol+"%s deref= %s (global) of type %s", inst, ptr, ptr.Type()))
		// Initialise global array/struct if needed.
		initNestedRefVar(infer, f, b, l, f.locals[ptr], true)
		return
	}
	if basic, ok := derefType(ptr.Type()).Underlying().(*types.Basic); ok && basic.Kind() == types.Byte {
		infer.Logger.Print(f.Sprintf(SubSymbol+"deref: %+v is a byte", ptr))
		// Create new byte instance here, bytes do no need explicit allocation.
		f.locals[ptr] = &Instance{ptr, f.InstanceID(), l.Index}
	}
	// Local.
	inst, ok := f.locals[ptr]
	if !ok {
		infer.Logger.Fatalf("deref: %s: %+v", ErrUnknownValue, ptr)
		return
	}
	f.locals[ptr], f.locals[val] = inst, inst
	infer.Logger.Print(f.Sprintf(ValSymbol+"%s deref= %s of type %s", val, ptr, ptr.Type()))
	// Initialise array/struct if needed.
	initNestedRefVar(infer, f, b, l, f.locals[ptr], false)
	return
}

func visitExtract(instr *ssa.Extract, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	if tupleInst, ok := f.locals[instr.Tuple]; ok {
		if _, ok := f.tuples[tupleInst]; !ok { // Tuple uninitialised
			infer.Logger.Fatalf("extract: %s: Unexpected tuple: %+v", ErrUnknownValue, instr)
			return
		}
		if inst := f.tuples[tupleInst][instr.Index]; inst == nil {
			f.tuples[tupleInst][instr.Index] = &Instance{instr, f.InstanceID(), l.Index}
		}
		f.locals[instr] = f.tuples[tupleInst][instr.Index]
		initNestedRefVar(infer, f, b, l, f.locals[instr], false)
		// Detect select tuple.
		if _, ok := f.selects[tupleInst]; ok {
			switch instr.Index {
			case 0:
				f.selects[tupleInst].Index = f.locals[instr]
				infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = extract select{%d} (select-index) for %s", f.locals[instr], instr.Index, instr))
				return
			default:
				infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = extract select{%d} for %s", f.locals[instr], instr.Index, instr))
				return
			}
		}
		// Detect commaok tuple.
		if commaOk, ok := f.commaok[tupleInst]; ok {
			switch instr.Index {
			case 0:
				infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = extract commaOk{%d} for %s", f.locals[instr], instr.Index, instr))
				return
			case 1:
				commaOk.OkCond = f.locals[instr]
				infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = extract commaOk{%d} (ok-test) for %s", f.locals[instr], instr.Index, instr))
				return
			}
		}
		infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = tuple %s[%d] of %d", f.locals[instr], tupleInst, instr.Index, len(f.tuples[tupleInst])))
		return
	}
}

func visitField(instr *ssa.Field, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	field, struc, index := instr, instr.X, instr.Field
	if sType, ok := struc.Type().Underlying().(*types.Struct); ok {
		sInst, ok := f.locals[struc]
		if !ok {
			infer.Logger.Fatalf("field: %s :%+v", ErrUnknownValue, struc)
			return
		}
		fields, ok := f.structs[sInst]
		if !ok {
			fields, ok = f.Prog.structs[sInst]
			if !ok {
				infer.Logger.Fatalf("field: %s: struct uninitialised %+v", ErrUnknownValue, sInst)
				return
			}
		}
		infer.Logger.Print(f.Sprintf(ValSymbol+"%s = %s"+FieldSymbol+"{%d} of type %s", instr.Name(), sInst, index, sType.String()))
		if fields[index] != nil {
			infer.Logger.Print(f.Sprintf(SubSymbol+"accessed as %s", fields[index]))
		} else {
			fields[index] = &Instance{field, f.InstanceID(), l.Index}
			infer.Logger.Print(f.Sprintf(SubSymbol+"field uninitialised, set to %s", field.Name()))
		}
		initNestedRefVar(infer, f, b, l, f.locals[field], false)
		f.locals[field] = fields[index]
		return
	}
	infer.Logger.Fatalf("field: %s: field is not struct: %+v", ErrInvalidVarRead, struc)
}

func visitFieldAddr(instr *ssa.FieldAddr, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	field, struc, index := instr, instr.X, instr.Field
	if sType, ok := derefType(struc.Type()).Underlying().(*types.Struct); ok {
		sInst, ok := f.locals[struc]
		if !ok {
			sInst, ok = f.Prog.globals[struc]
			if !ok {
				infer.Logger.Fatalf("field-addr: %s: %+v", ErrUnknownValue, struc)
				return
			}
		}
		// Check status of instance.
		switch inst := sInst.(type) {
		case *Instance: // Continue
		case *ExtInstance: // Continue
			infer.Logger.Print(f.Sprintf(SubSymbol+"field-addr: %+v is external", sInst))
			f.locals[field] = inst
			return
		case *ConstInstance:
			infer.Logger.Print(f.Sprintf(SubSymbol+"field-addr: %+v is a constant", sInst))
			if inst.Const.IsNil() {
				f.locals[field] = inst
			}
			return
		default:
			infer.Logger.Fatalf("field-addr: %s: not instance %+v", ErrUnknownValue, sInst)
			return
		}
		// Find the struct.
		fields, ok := f.structs[sInst]
		if !ok {
			fields, ok = f.Prog.structs[sInst]
			if !ok {
				infer.Logger.Fatalf("field-addr: %s: struct uninitialised %+v", ErrUnknownValue, sInst)
				return
			}
		}
		infer.Logger.Print(f.Sprintf(ValSymbol+"%s = %s"+FieldSymbol+"{%d} of type %s", instr.Name(), sInst, index, sType.String()))
		if fields[index] != nil {
			infer.Logger.Print(f.Sprintf(SubSymbol+"accessed as %s:%s", fields[index], fields[index].Var().Type()))
		} else {
			fields[index] = &Instance{field, f.InstanceID(), l.Index}
			infer.Logger.Print(f.Sprintf(SubSymbol+"field uninitialised, set to %s", field.Name()))
		}
		initNestedRefVar(infer, f, b, l, fields[index], false)
		f.locals[field] = fields[index]
		return
	}
	infer.Logger.Fatalf("field-addr: %s: field is not struct: %+v", ErrInvalidVarRead, struc)
}

func visitGo(instr *ssa.Go, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	infer.Logger.Printf(f.Sprintf(SpawnSymbol+"%s %s", fmtSpawn("spawn"), instr))
	f.Go(instr, infer)
}

func visitIf(instr *ssa.If, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	if len(instr.Block().Succs) != 2 {
		infer.Logger.Fatal(ErrInvalidIfSucc)
	}
	// Detect and unroll l.
	if l.State != NonLoop && l.Bound == Static && instr.Cond == l.CondVar {
		if l.HasNext() {
			infer.Logger.Printf(f.Sprintf(LoopSymbol+"loop continue %s", l))
			visitBasicBlock(instr.Block().Succs[0], infer, f, NewBlock(f, instr.Block().Succs[0], b.Index), l)
		} else {
			infer.Logger.Printf(f.Sprintf(LoopSymbol+"loop exit %s", l))
			//f.Visited[instr.Block()] = 0
			visitBasicBlock(instr.Block().Succs[1], infer, f, NewBlock(f, instr.Block().Succs[1], b.Index), l)
		}
		return
	}
	// Detect Select branches.
	if bin, ok := instr.Cond.(*ssa.BinOp); ok && bin.Op == token.EQL {
		for _, sel := range f.selects {
			if bin.X == sel.Index.Var() {
				if i, ok := bin.Y.(*ssa.Const); ok && i.Value.Kind() == constant.Int {
					//infer.Logger.Print(fmt.Sprintf("[select-%d]", i.Int64()), f.FuncDef.String())
					parDef := f.FuncDef
					parDef.PutAway() // Save select
					visitBasicBlock(instr.Block().Succs[0], infer, f, NewBlock(f, instr.Block().Succs[0], b.Index), l)
					f.FuncDef.PutAway() // Save case
					selCase, err := f.FuncDef.Restore()
					if err != nil {
						infer.Logger.Fatal("select-case:", err)
					}
					sel.MigoStmt.Cases[i.Int64()] = append(sel.MigoStmt.Cases[i.Int64()], selCase...)
					selParent, err := parDef.Restore()
					if err != nil {
						infer.Logger.Fatal("select-parent:", err)
					}
					parDef.AddStmts(selParent...)

					// Test if select has default branch & if this is default
					if !sel.Instr.Blocking && i.Int64() == int64(len(sel.Instr.States)-1) {
						infer.Logger.Print(f.Sprintf(SelectSymbol + "default"))
						parDef := f.FuncDef
						parDef.PutAway() // Save select
						visitBasicBlock(instr.Block().Succs[1], infer, f, NewBlock(f, instr.Block().Succs[1], b.Index), l)
						f.FuncDef.PutAway() // Save case
						selDefault, err := f.FuncDef.Restore()
						if err != nil {
							infer.Logger.Fatal("select-default:", err)
						}
						sel.MigoStmt.Cases[len(sel.MigoStmt.Cases)-1] = append(sel.MigoStmt.Cases[len(sel.MigoStmt.Cases)-1], selDefault...)
						selParent, err := parDef.Restore()
						if err != nil {
							infer.Logger.Fatal("select-parent:", err)
						}
						parDef.AddStmts(selParent...)
					} else {
						infer.Logger.Printf(f.Sprintf(IfSymbol+"select-else "+JumpSymbol+"%d", instr.Block().Succs[1].Index))
						visitBasicBlock(instr.Block().Succs[1], infer, f, NewBlock(f, instr.Block().Succs[1], b.Index), l)
					}
					return // Select if-then-else handled
				}
			}
		}
	}

	var cond string
	if inst, ok := f.locals[instr.Cond]; ok && isCommaOk(f, inst) {
		cond = fmt.Sprintf("comma-ok %s", instr.Cond.Name())
	} else {
		cond = fmt.Sprintf("%s", instr.Cond.Name())
	}

	// Save parent.
	f.FuncDef.PutAway()
	f.FuncDef.AddStmts(&migo.TauStatement{})
	infer.Logger.Printf(f.Sprintf(IfSymbol+"if %s then"+JumpSymbol+"%d", cond, instr.Block().Succs[0].Index))
	visitBasicBlock(instr.Block().Succs[0], infer, f, NewBlock(f, instr.Block().Succs[0], b.Index), l)
	// Save then.
	f.FuncDef.PutAway()
	f.FuncDef.AddStmts(&migo.TauStatement{})
	infer.Logger.Printf(f.Sprintf(IfSymbol+"if %s else"+JumpSymbol+"%d", cond, instr.Block().Succs[1].Index))
	visitBasicBlock(instr.Block().Succs[1], infer, f, NewBlock(f, instr.Block().Succs[1], b.Index), l)
	// Save else.
	f.FuncDef.PutAway()
	elseStmts, err := f.FuncDef.Restore() // Else
	if err != nil {
		infer.Logger.Fatal("restore else:", err)
	}
	thenStmts, err := f.FuncDef.Restore() // Then
	if err != nil {
		infer.Logger.Fatal("restore then:", err)
	}
	parentStmts, err := f.FuncDef.Restore() // Parent
	if err != nil {
		infer.Logger.Fatal("restore if-then-else parent:", err)
	}
	f.FuncDef.AddStmts(parentStmts...)

	emptyStmt := false
	if len(thenStmts) == 1 {
		if _, ok := thenStmts[0].(*migo.TauStatement); ok {
			emptyStmt = true
		}
	}
	if len(elseStmts) == 1 {
		if _, ok := elseStmts[0].(*migo.TauStatement); ok {
			emptyStmt = emptyStmt && true
		}
	}
	if emptyStmt {
		f.FuncDef.AddStmts(&migo.TauStatement{})
		return
	}
	f.FuncDef.AddStmts(&migo.IfStatement{Then: thenStmts, Else: elseStmts})
}

func visitIndex(instr *ssa.Index, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	elem, array, index := instr, instr.X, instr.Index
	// Array.
	if aType, ok := array.Type().Underlying().(*types.Array); ok {
		aInst, ok := f.locals[array]
		if !ok {
			aInst, ok = f.Prog.globals[array]
			if !ok {
				infer.Logger.Fatalf("index: %s: array %+v", ErrUnknownValue, array)
				return
			}
		}
		elems, ok := f.arrays[aInst]
		if !ok {
			elems, ok = f.Prog.arrays[aInst]
			if !ok {
				infer.Logger.Fatalf("index: %s: not an array %+v", ErrUnknownValue, aInst)
				return
			}
		}
		infer.Logger.Print(f.Sprintf(ValSymbol+"%s = %s"+FieldSymbol+"[%s] of type %s", instr.Name(), aInst, index, aType.String()))
		if elems[index] != nil {
			infer.Logger.Print(f.Sprintf(SubSymbol+"accessed as %s", elems[index]))
		} else {
			elems[index] = &Instance{elem, f.InstanceID(), l.Index}
			infer.Logger.Printf(f.Sprintf(SubSymbol+"elem uninitialised, set to %s", elem.Name()))
		}
		initNestedRefVar(infer, f, b, l, elems[index], false)
		f.locals[elem] = elems[index]
		return
	}
}

func visitIndexAddr(instr *ssa.IndexAddr, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	elem, array, index := instr, instr.X, instr.Index
	// Array.
	if aType, ok := derefType(array.Type()).Underlying().(*types.Array); ok {
		aInst, ok := f.locals[array]
		if !ok {
			aInst, ok = f.Prog.globals[array]
			if !ok {
				infer.Logger.Fatalf("index-addr: %s: array %+v", ErrUnknownValue, array)
				return
			}
		}
		// Check status of instance.
		switch inst := aInst.(type) {
		case *Instance: // Continue
		case *ExtInstance: // External
			infer.Logger.Print(f.Sprintf(SubSymbol+"index-addr: array %+v is external", aInst))
			f.locals[elem] = inst
		case *ConstInstance:
			infer.Logger.Print(f.Sprintf(SubSymbol+"index-addr: array %+v is a constant", aInst))
			if inst.Const.IsNil() {
				f.locals[elem] = inst
			}
			return
		default:
			infer.Logger.Fatalf("index-addr: %s: array is not instance %+v", ErrUnknownValue, aInst)
			return
		}
		// Find the array.
		elems, ok := f.arrays[aInst]
		if !ok {
			elems, ok = f.Prog.arrays[aInst]
			if !ok {
				infer.Logger.Fatalf("index-addr: %s: array uninitialised %s", ErrUnknownValue, aInst)
				return
			}
		}
		infer.Logger.Print(f.Sprintf(ValSymbol+"%s = %s"+FieldSymbol+"[%s] of type %s", instr.Name(), aInst, index, aType.String()))
		if elems[index] != nil {
			infer.Logger.Print(f.Sprintf(SubSymbol+"accessed as %s", elems[index]))
		} else {
			elems[index] = &Instance{elem, f.InstanceID(), l.Index}
			infer.Logger.Printf(f.Sprintf(SubSymbol+"elem uninitialised, set to %s", elem.Name()))
		}
		initNestedRefVar(infer, f, b, l, elems[index], false)
		f.locals[elem] = elems[index]
		return
	}
	// Slices.
	if sType, ok := derefType(array.Type()).Underlying().(*types.Slice); ok {
		sInst, ok := f.locals[array]
		if !ok {
			sInst, ok = f.Prog.globals[array]
			if !ok {
				infer.Logger.Fatalf("index-addr: %s: slice %+v", ErrUnknownValue, array)
				return
			}
		}
		// Check status of instance.
		switch inst := sInst.(type) {
		case *Instance: // Continue
			if basic, ok := sType.Elem().(*types.Basic); ok && basic.Kind() == types.Byte {
				f.locals[elem] = inst
				return
			}
		case *ExtInstance: // External
			infer.Logger.Print(f.Sprintf(SubSymbol+"index-addr: slice %+v is external", sInst))
			f.locals[elem] = inst
			return
		case *ConstInstance:
			infer.Logger.Print(f.Sprintf(SubSymbol+"index-addr: slice %+v is a constant", sInst))
			if inst.Const.IsNil() {
				f.locals[elem] = inst
			}
			return
		default:
			infer.Logger.Fatalf("index-addr: %s: slice is not instance %+v", ErrUnknownValue, sInst)
			return
		}
		// Find the slice.
		elems, ok := f.arrays[sInst]
		if !ok {
			elems, ok = f.Prog.arrays[sInst]
			if !ok {
				infer.Logger.Fatalf("index-addr: %s: slice uninitialised %+v", ErrUnknownValue, sInst)
				return
			}
		}
		infer.Logger.Print(f.Sprintf(ValSymbol+"%s = %s"+FieldSymbol+"[%s] (slice) of type %s", instr.Name(), sInst, index, sType.String()))
		if elems[index] != nil {
			infer.Logger.Print(f.Sprintf(SubSymbol+"accessed as %s", elems[index]))
		} else {
			elems[index] = &Instance{elem, f.InstanceID(), l.Index}
			infer.Logger.Printf(f.Sprintf(SubSymbol+"elem uninitialised, set to %s", elem.Name()))
		}
		initNestedRefVar(infer, f, b, l, elems[index], false)
		f.locals[elem] = elems[index]
		return
	}
	infer.Logger.Fatalf("index-addr: %s: not array/slice %+v", ErrInvalidVarRead, array)
}

func visitJump(jump *ssa.Jump, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	if len(jump.Block().Succs) != 1 {
		infer.Logger.Fatal(ErrInvalidJumpSucc)
	}
	curr, next := jump.Block(), jump.Block().Succs[0]
	infer.Logger.Printf(f.Sprintf(SkipSymbol+"block %d%s%d", curr.Index, fmtLoopHL(JumpSymbol), next.Index))
	switch l.State {
	case Exit:
		l.State = NonLoop
	}
	if len(next.Preds) > 1 {
		infer.Logger.Printf(f.Sprintf(SplitSymbol+"Jump (%d ⇾ %d) %s", curr.Index, next.Index, l.String()))
		var stmt *migo.CallStatement
		if l.Bound == Static && l.HasNext() {
			stmt = &migo.CallStatement{Name: fmt.Sprintf("%s#%d_loop%d", f.Fn.String(), next.Index, l.Index), Params: []*migo.Parameter{}}
		} else {
			stmt = &migo.CallStatement{Name: fmt.Sprintf("%s#%d", f.Fn.String(), next.Index), Params: []*migo.Parameter{}}
		}
		for _, s := range f.FuncDef.Stmts {
			if nc, ok := s.(*migo.NewChanStatement); ok {
				stmt.AddParams(&migo.Parameter{Caller: nc.Name, Callee: nc.Name})
			}
		}
		for _, p := range f.FuncDef.Params {
			stmt.AddParams(&migo.Parameter{Caller: p.Callee, Callee: p.Callee})
		}
		f.FuncDef.AddStmts(stmt)
		if _, visited := f.Visited[next]; !visited {
			newBlock := NewBlock(f, next, b.Index)
			oldFunc, newFunc := f.FuncDef, newBlock.MigoDef
			if l.Bound == Static && l.HasNext() {
				newFunc = migo.NewFunction(fmt.Sprintf("%s#%d_loop%d", f.Fn.String(), next.Index, l.Index))
			}
			for _, p := range stmt.Params {
				newFunc.AddParams(&migo.Parameter{Caller: p.Callee, Callee: p.Callee})
			}
			f.FuncDef = newFunc
			infer.Env.MigoProg.AddFunction(newFunc)
			visitBasicBlock(next, infer, f, newBlock, l)
			f.FuncDef = oldFunc
			return
		}
	}
	visitBasicBlock(next, infer, f, NewBlock(f, next, b.Index), l)
}

func visitLookup(instr *ssa.Lookup, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	v, ok := f.locals[instr.X]
	if !ok {
		if c, ok := instr.X.(*ssa.Const); ok {
			f.locals[instr.X] = &ConstInstance{c}
			v = f.locals[instr.X]
		} else {
			infer.Logger.Fatalf("lookup: %s: %+v", ErrUnknownValue, instr.X)
			return
		}
	}
	// Lookup test.
	idx, ok := f.locals[instr.Index]
	if !ok {
		if c, ok := instr.Index.(*ssa.Const); ok {
			idx = &ConstInstance{c}
		} else {
			idx = &Instance{instr.Index, f.InstanceID(), l.Index}
		}
		f.locals[instr.Index] = idx
	}
	f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
	initNestedRefVar(infer, f, b, l, f.locals[instr], false)
	if instr.CommaOk {
		f.commaok[f.locals[instr]] = &CommaOk{Instr: instr, Result: f.locals[instr]}
		f.tuples[f.locals[instr]] = make(Tuples, 2) // { elem, lookupOk }
	}
	infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = lookup %s[%s]", f.locals[instr], v, idx))
}

func visitMakeChan(instr *ssa.MakeChan, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	newch := &Instance{instr, f.InstanceID(), l.Index}
	f.locals[instr] = newch
	chType, ok := instr.Type().(*types.Chan)
	if !ok {
		infer.Logger.Fatal(ErrMakeChanNonChan)
	}
	bufSz, ok := instr.Size.(*ssa.Const)
	if !ok {
		infer.Logger.Fatal(ErrNonConstChanBuf)
	}
	infer.Logger.Printf(f.Sprintf(ChanSymbol+"%s = %s {t:%s, buf:%d} @ %s",
		newch,
		fmtChan("chan"),
		chType.Elem(),
		bufSz.Int64(),
		fmtPos(infer.SSA.FSet.Position(instr.Pos()).String())))
	f.FuncDef.AddStmts(&migo.NewChanStatement{Name: instr, Chan: newch.String(), Size: bufSz.Int64()})
}

func visitMakeClosure(instr *ssa.MakeClosure, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
	f.Prog.closures[f.locals[instr]] = make(Captures, len(instr.Bindings))
	for i, binding := range instr.Bindings {
		f.Prog.closures[f.locals[instr]][i] = f.locals[binding]
	}
	infer.Logger.Print(f.Sprintf(NewSymbol+"%s = make closure", f.locals[instr]))
}

func visitMakeInterface(instr *ssa.MakeInterface, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	iface, ok := f.locals[instr.X]
	if !ok {
		if c, ok := instr.X.(*ssa.Const); ok {
			f.locals[instr.X] = &ConstInstance{c}
		} else {
			infer.Logger.Fatalf("make-iface: %s: %s", ErrUnknownValue, instr.X)
			return
		}
	}
	f.locals[instr] = iface
	infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = make-iface %s", f.locals[instr], instr.String()))
}

func visitMakeMap(instr *ssa.MakeMap, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
	f.maps[f.locals[instr]] = make(map[VarInstance]VarInstance)
	infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = make-map", f.locals[instr]))
}

func visitMakeSlice(instr *ssa.MakeSlice, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
	f.arrays[f.locals[instr]] = make(Elems)
	infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = make-slice", f.locals[instr]))
}

func visitMapUpdate(instr *ssa.MapUpdate, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	inst, ok := f.locals[instr.Map]
	if !ok {
		infer.Logger.Fatalf("map-update: %s: %s", ErrUnknownValue, instr.Map)
		return
	}
	m, ok := f.maps[inst]
	if !ok {
		f.maps[inst] = make(map[VarInstance]VarInstance) // XXX This shouldn't happen
		m = f.maps[inst]                                 // The map must be defined somewhere we skipped
		infer.Logger.Printf("map-update: uninitialised map: %+v %s", instr.Map, instr.Map.String())
	}
	k, ok := f.locals[instr.Key]
	if !ok {
		k = &Instance{instr.Key, f.InstanceID(), l.Index}
		f.locals[instr.Key] = k
	}
	v, ok := f.locals[instr.Value]
	if !ok {
		if c, ok := instr.Value.(*ssa.Const); ok {
			v = &ConstInstance{c}
		} else {
			v = &Instance{instr.Value, f.InstanceID(), l.Index}
		}
		f.locals[instr.Value] = v
	}
	m[k] = v
	infer.Logger.Printf(f.Sprintf(SkipSymbol+"%s[%s] = %s", inst, k, v))
}

func visitNext(instr *ssa.Next, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
	f.tuples[f.locals[instr]] = make(Tuples, 3) // { ok, k, v}
	infer.Logger.Print(f.Sprintf(SkipSymbol+"%s (ok, k, v) = next", f.locals[instr]))
}

func visitPhi(instr *ssa.Phi, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	loopDetectBounds(instr, infer, f, b, l)
}

func visitRecv(instr *ssa.UnOp, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index} // received value
	ch, ok := f.locals[instr.X]
	if !ok { // Channel does not exist
		infer.Logger.Fatalf("recv: %s: %+v", ErrUnknownValue, instr.X)
		return
	}
	// Receive test.
	if instr.CommaOk {
		f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
		f.commaok[f.locals[instr]] = &CommaOk{Instr: instr, Result: f.locals[instr]}
		f.tuples[f.locals[instr]] = make(Tuples, 2) // { recvVal, recvOk }
	}
	pos := infer.SSA.DecodePos(ch.(*Instance).Pos())
	infer.Logger.Print(f.Sprintf(RecvSymbol+"%s = %s @ %s", f.locals[instr], ch, fmtPos(pos)))
	c := getChan(f.locals[instr.X].Var(), infer)
	f.FuncDef.AddStmts(&migo.RecvStatement{Chan: c.Name()})
	f.FuncDef.AddParams(&migo.Parameter{Caller: c, Callee: c})
	// Initialise received value if needed.
	initNestedRefVar(infer, f, b, l, f.locals[instr], false)
}

func visitReturn(ret *ssa.Return, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	if b.MigoDef.IsEmpty() {
		b.MigoDef.AddStmts(&migo.TauStatement{})
	}
	switch len(ret.Results) {
	case 0:
		infer.Logger.Printf(f.Sprintf(ReturnSymbol))
	case 1:
		if c, ok := ret.Results[0].(*ssa.Const); ok {
			f.locals[ret.Results[0]] = &ConstInstance{c}
		}
		res, ok := f.locals[ret.Results[0]]
		if !ok {
			infer.Logger.Printf("Returning uninitialised value %s/%s", ret.Results[0].Name(), f.locals[ret.Results[0]])
			return
		}
		f.retvals = append(f.retvals, f.locals[ret.Results[0]])
		infer.Logger.Printf(f.Sprintf(ReturnSymbol+"return[1] %s %v", res, f.retvals))
	default:
		for _, res := range ret.Results {
			f.retvals = append(f.retvals, f.locals[res])
		}
		infer.Logger.Printf(f.Sprintf(ReturnSymbol+"return[%d] %v", len(ret.Results), f.retvals))
	}
}

func visitRunDefers(instr *ssa.RunDefers, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	for i := len(f.defers) - 1; i >= 0; i-- {
		common := f.defers[i].Common()
		callee := f.prepareCallFn(common, common.StaticCallee(), nil)
		visitFunc(callee.Fn, infer, callee)
		if callee.HasBody() {
			callStmt := &migo.CallStatement{Name: callee.Fn.String(), Params: []*migo.Parameter{}}
			for i, c := range common.Args {
				if _, ok := c.Type().(*types.Chan); ok {
					callStmt.AddParams(&migo.Parameter{Caller: c, Callee: callee.Fn.Params[i]})
				}
			}
			callee.FuncDef.AddStmts(callStmt)
		}
	}
}

func visitSelect(instr *ssa.Select, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
	f.selects[f.locals[instr]] = &Select{
		Instr:    instr,
		MigoStmt: &migo.SelectStatement{Cases: [][]migo.Statement{}},
	}
	selStmt := f.selects[f.locals[instr]].MigoStmt
	for _, sel := range instr.States {
		ch, ok := f.locals[sel.Chan]

		if !ok {
			infer.Logger.Print("Select found an unknown channel", sel.Chan.String())
		}
		var stmt migo.Statement
		c := getChan(ch.Var(), infer)
		switch sel.Dir {
		case types.SendOnly:
			stmt = &migo.SendStatement{Chan: c.Name()}
		case types.RecvOnly:
			stmt = &migo.RecvStatement{Chan: c.Name()}
		}
		f.FuncDef.AddParams(&migo.Parameter{Caller: c, Callee: c})
		selStmt.Cases = append(selStmt.Cases, []migo.Statement{stmt})
	}
	// Default case exists.
	if !instr.Blocking {
		selStmt.Cases = append(selStmt.Cases, []migo.Statement{&migo.TauStatement{}})
	}
	f.tuples[f.locals[instr]] = make(Tuples, 2+len(selStmt.Cases)) // index + recvok + cases
	f.FuncDef.AddStmts(selStmt)
	infer.Logger.Print(f.Sprintf(SelectSymbol+" %d cases %s = %s", 2+len(selStmt.Cases), instr.Name(), instr.String()))
}

func visitSend(instr *ssa.Send, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	ch, ok := f.locals[instr.Chan]
	if !ok {
		infer.Logger.Fatalf("send: %s: %+v", ErrUnknownValue, instr.Chan)
	}
	pos := infer.SSA.DecodePos(ch.(*Instance).Pos())
	infer.Logger.Printf(f.Sprintf(SendSymbol+"%s @ %s", ch, fmtPos(pos)))
	c := getChan(f.locals[instr.Chan].Var(), infer)
	f.FuncDef.AddStmts(&migo.SendStatement{Chan: c.Name()})
	f.FuncDef.AddParams(&migo.Parameter{Caller: c, Callee: c})
}

func visitSkip(instr ssa.Instruction, infer *TypeInfer, f *Function, b *Block, loop *Loop) {
	if v, isVal := instr.(ssa.Value); isVal {
		infer.Logger.Printf(f.Sprintf(SkipSymbol+"%T\t%s = %s", v, v.Name(), v.String()))
		return
	}
	infer.Logger.Printf(f.Sprintf(SkipSymbol+"%T\t%s", instr, instr))
}

func visitSlice(instr *ssa.Slice, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
	if _, ok := f.locals[instr.X]; !ok {
		infer.Logger.Fatalf("slice: %s: %+v", ErrUnknownValue, instr.X)
		return
	}
	if basic, ok := instr.Type().Underlying().(*types.Basic); ok && basic.Kind() == types.String {
		infer.Logger.Printf(f.Sprintf(SkipSymbol+"%s = slice on string, skipping", f.locals[instr]))
		return
	}
	if slice, ok := instr.Type().Underlying().(*types.Slice); ok {
		if basic, ok := slice.Elem().Underlying().(*types.Basic); ok && basic.Kind() == types.Byte {
			infer.Logger.Printf(f.Sprintf(SkipSymbol+"%s = slice on byte, skipping", f.locals[instr]))
			return
		}
	}
	aInst, ok := f.arrays[f.locals[instr.X]]
	if !ok {
		aInst, ok = f.Prog.arrays[f.locals[instr.X]]
		if !ok {
			switch f.locals[instr.X].(type) {
			case *Instance: // Continue
				infer.Logger.Fatalf("slice: %s: non-slice %+v", ErrUnknownValue, instr.X)
				return
			case *ConstInstance:
				f.arrays[f.locals[instr.X]] = make(Elems)
				aInst = f.arrays[f.locals[instr.X]]
				infer.Logger.Print(f.Sprintf("slice: const %s %s", instr.X.Name(), f.locals[instr.X]))
				return
			}
		}
		f.Prog.arrays[f.locals[instr]] = aInst
		infer.Logger.Print(f.Sprintf(ValSymbol+"%s = slice %s", f.locals[instr], f.locals[instr.X]))
		return
	}
	f.arrays[f.locals[instr]] = aInst
	infer.Logger.Print(f.Sprintf(ValSymbol+"%s = slice %s", f.locals[instr], f.locals[instr.X]))
}

func visitStore(instr *ssa.Store, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	source, dstPtr := instr.Val, instr.Addr
	// Global.
	if _, ok := dstPtr.(*ssa.Global); ok {
		dstInst, ok := f.Prog.globals[dstPtr]
		if !ok {
			infer.Logger.Fatalf("store (global): %s: %+v", ErrUnknownValue, dstPtr)
		}
		inst, ok := f.locals[source]
		if !ok {
			inst, ok = f.Prog.globals[source]
			if !ok {
				if c, ok := source.(*ssa.Const); ok {
					inst = &ConstInstance{c}
				} else {
					infer.Logger.Fatalf("store (global): %s: %+v", ErrUnknownValue, source)
				}
			}
		}
		f.Prog.globals[dstPtr] = inst
		switch source.Type().Underlying().(type) {
		case *types.Array:
			f.updateInstances(dstInst, inst)
		case *types.Slice:
			f.updateInstances(dstInst, inst)
		case *types.Struct:
			f.updateInstances(dstInst, inst)
		case *types.Map:
			f.updateInstances(dstInst, inst)
		default:
			// Nothing to update.
		}
		infer.Logger.Print(f.Sprintf(ValSymbol+"%s = %s (global)", dstPtr.Name(), f.locals[source]))
		return
	}
	if basic, ok := derefType(dstPtr.Type()).Underlying().(*types.Basic); ok && basic.Kind() == types.Byte {
		infer.Logger.Print(f.Sprintf(SubSymbol+"store: %+v is a byte", dstPtr))
		f.locals[dstPtr] = &Instance{dstPtr, f.InstanceID(), l.Index}
	}
	// Local.
	dstInst, ok := f.locals[dstPtr]
	if !ok {
		infer.Logger.Fatalf("store: addr %s: %+v", ErrUnknownValue, dstPtr)
	}
	inst, ok := f.locals[source]
	if !ok {
		if c, ok := source.(*ssa.Const); ok {
			inst = &ConstInstance{c}
		} else {
			infer.Logger.Printf("store: val %s%s: %s", source.Name(), source.Type(), ErrUnknownValue)
		}
	}
	f.locals[dstPtr] = inst
	switch source.Type().Underlying().(type) {
	case *types.Array:
		f.updateInstances(dstInst, inst)
	case *types.Slice:
		f.updateInstances(dstInst, inst)
	case *types.Struct:
		f.updateInstances(dstInst, inst)
	case *types.Map:
		f.updateInstances(dstInst, inst)
	default:
		// Nothing to update.
	}
	infer.Logger.Print(f.Sprintf(ValSymbol+"*%s store= %s/%s", dstPtr.Name(), source.Name(), f.locals[source]))
	return
}

func visitTypeAssert(instr *ssa.TypeAssert, infer *TypeInfer, f *Function, b *Block, l *Loop) {
	if iface, ok := instr.AssertedType.(*types.Interface); ok {
		if meth, _ := types.MissingMethod(instr.X.Type(), iface, true); meth == nil { // No missing methods
			inst, ok := f.locals[instr.X]
			if !ok {
				infer.Logger.Fatalf("typeassert: %s: iface X %+v", ErrUnknownValue, instr.X.Name())
				return
			}
			if instr.CommaOk {
				f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
				f.commaok[f.locals[instr]] = &CommaOk{Instr: instr, Result: f.locals[instr]}
				f.tuples[f.locals[instr]] = make(Tuples, 2)
				infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = typeassert iface %s commaok", f.locals[instr], inst))
				return
			}
			f.locals[instr] = inst
			infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = typeassert iface %s", f.locals[instr], inst))
			return
		}
		infer.Logger.Fatalf("typeassert: %s: %+v", ErrMethodNotFound, instr)
		return
	}
	inst, ok := f.locals[instr.X]
	if !ok {
		infer.Logger.Fatalf("typeassert: %s: assert from %+v", ErrUnknownValue, instr.X)
		return
	}
	if instr.CommaOk {
		f.locals[instr] = &Instance{instr, f.InstanceID(), l.Index}
		f.commaok[f.locals[instr]] = &CommaOk{Instr: instr, Result: f.locals[instr]}
		f.tuples[f.locals[instr]] = make(Tuples, 2)
		infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = typeassert %s commaok", f.locals[instr], inst))
		return
	}
	f.locals[instr] = inst
	infer.Logger.Print(f.Sprintf(SkipSymbol+"%s = typeassert %s", f.locals[instr], f.locals[instr.X]))
	return
	//infer.Logger.Fatalf("typeassert: %s: %+v", ErrIncompatType, instr)
}
