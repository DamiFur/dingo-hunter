// dingo-hunter: A tool for analysing Go code to extract the communication
// patterns for deadlock analysis.
//
// The tool currently only works for commands as the analysis uses the main
// function as entry point.
package main

// This file contains only the functions needed to start the analysis
//  - Handle command line flags
//  - Set up session variables

import (
	"flag"
	"fmt"
	"go/build"
	"os"
	"time"

	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/go/types"

	"github.com/nickng/dingo-hunter/sesstype"
	"github.com/nickng/dingo-hunter/utils"
)

var (
	session *sesstype.Session // Keeps track of the all session
	ssaflag = ssa.BuilderModeFlag(flag.CommandLine, "ssa", ssa.BareInits)
	goQueue = make([]*frame, 0)
)

const usage = "Usage dingo-hunter <main.go> ...\n"

// main function analyses the program in four steps
//
// (1) Load program as SSA
// (2) Analyse main.main()
// (3) Analyse goroutines found in (2)
// (4) Output results
func main() {
	var prog *ssa.Program
	var err error

	startTime := time.Now()

	prog, err = loadSSA()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading files: %s\n", err)
	}

	mainPkg := findMainPkg(prog)
	if mainPkg == nil {
		fmt.Fprintf(os.Stderr, "Error: 'main' package not found\n")
		os.Exit(1)
	}

	session = sesstype.CreateSession() // init needs Session
	init := mainPkg.Func("init")
	main := mainPkg.Func("main")

	fr := makeToplevelFrame()
	for _, pkg := range prog.AllPackages() {
		for _, memb := range pkg.Members {
			switch val := memb.(type) {
			case *ssa.Global:
				switch derefAll(val.Type()).(type) {
				case *types.Array:
					vd := utils.NewVarDef(val)
					fr.env.globals[val] = vd
					fr.env.arrays[vd] = make(Elems)

				case *types.Struct:
					vd := utils.NewVarDef(val)
					fr.env.globals[val] = vd
					fr.env.structs[vd] = make(Fields)

				case *types.Chan:
					var c *types.Chan
					vd := utils.NewVarDef(EmptyValue{T: c})
					fr.env.globals[val] = vd

				default:
					fr.env.globals[val] = utils.NewVarDef(val)
				}
			}
		}
	}

	fmt.Fprintf(os.Stderr, "++ call.toplevel %s()\n", orange("init"))
	visitFunc(init, fr)
	if main == nil {
		fmt.Fprintf(os.Stderr, "Error: 'main()' function not found in 'main' package\n")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "++ call.toplevel %s()\n", orange("main"))
	visitFunc(main, fr)

	fr.env.session.Types[fr.gortn.role] = fr.gortn.root

	var goFrm *frame
	for len(goQueue) > 0 {
		goFrm, goQueue = goQueue[0], goQueue[1:]
		fmt.Fprintf(os.Stderr, "\n%s\n\n", goFrm.fn.Name())
		visitFunc(goFrm.fn, goFrm)
		goFrm.env.session.Types[goFrm.gortn.role] = goFrm.gortn.root
	}

	elapsedTime := time.Since(startTime)

	fmt.Printf("Analysis time: %f\n", elapsedTime.Seconds())

	fmt.Printf(" ----- Results ----- \n%s\n", session.String())

	sesstype.GenDot(session)
	sesstype.GenAllCFSMs(session)
	sesstype.PrintNodeSummary(session)
	sesstype.PrintCFSMSummary()
}

// Load command line arguments as SSA program for analysis
func loadSSA() (*ssa.Program, error) {
	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	var conf = loader.Config{Build: &build.Default}

	// Use the initial packages from the command line.
	if _, err := conf.FromArgs(args /*test?*/, false); err != nil {
		return nil, err
	}

	// Load, parse and type-check the whole program.
	prog, err := conf.Load()
	if err != nil {
		return nil, err
	}
	progSSA := ssautil.CreateProgram(prog, *ssaflag) // If ssabuild specified

	// Build and display only the initial packages (and synthetic wrappers),
	// unless -run is specified.
	//
	// Adapted from golang.org/x/tools/go/ssa
	for _, info := range prog.InitialPackages() {
		progSSA.Package(info.Pkg).Build()
	}

	// Don't load these packages.
	for _, info := range prog.AllPackages {
		if info.Pkg.Name() != "fmt" && info.Pkg.Name() != "reflect" && info.Pkg.Name() != "strings" && info.Pkg.Name() != "runtime" && info.Pkg.Name() != "sync" {
			progSSA.Package(info.Pkg).Build()
		}
	}

	return progSSA, nil
}

func findMainPkg(prog *ssa.Program) *ssa.Package {
	pkgs := prog.AllPackages()
	for _, pkg := range pkgs {
		if pkg.Pkg.Name() == "main" {
			return pkg
		}
	}

	return nil
}
