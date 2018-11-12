// Copyright © 2016 Nicholas Ng <nickng@projectfate.org>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"log"
	"os"

	"fmt"

	"github.com/damifur/dingo-hunter/logwriter"
	"github.com/damifur/dingo-hunter/migoextract"
	"github.com/damifur/dingo-hunter/ssabuilder"
	"github.com/spf13/cobra"
)

var (
	outfile string // Path to output file
)

// migoCmd represents the analyse command
var migoCmd = &cobra.Command{
	Use:   "migo",
	Short: "Extract MiGo types from source code",
	Long: `Extract MiGo types from source code

The inputs should be a list of .go files in the same directory (of package main)
One of the .go file should contain the main function.`,
	Run: func(cmd *cobra.Command, args []string) {
		extractMigo(args)
	},
}

func init() {
	migoCmd.Flags().StringVar(&outfile, "output", "", "output migo file")

	RootCmd.AddCommand(migoCmd)
}

func extractMigo(files []string) {

	fmt.Println("Entre al extractMIGO!!!!!!!!!!")

	logFile, err := RootCmd.PersistentFlags().GetString("log")
	if err != nil {
		log.Fatal(err)
	}
	noLogging, err := RootCmd.PersistentFlags().GetBool("no-logging")
	if err != nil {
		log.Fatal(err)
	}
	noColour, err := RootCmd.PersistentFlags().GetBool("no-colour")
	if err != nil {
		log.Fatal(err)
	}
	l := logwriter.NewFile(logFile, !noLogging, !noColour)
	if err := l.Create(); err != nil {
		log.Fatal(err)
	}
	defer l.Cleanup()

	conf, err := ssabuilder.NewConfig(files)
	if err != nil {
		log.Fatal(err)
	}
	conf.BuildLog = l.Writer
	ssainfo, err := conf.Build()
	f := ssainfo.CallGraph().Func
	children := ssainfo.CallGraph().Children[0].Func
	// ACA ESTA EL PATH DE LA FUNCION CON LA LINEA DONDE ARRANCA LA FUNC MAIN
	fmt.Println(f.Blocks[0].Instrs)
	fmt.Println("-----------------------------")
	fmt.Println(f.Prog.Fset.Position(f.Pos()))
	fmt.Println(children.Prog.Fset.Position(children.Pos())) //f.Prog.Fset.File(f.Pos()))
	if err != nil {
		log.Fatal(err)
	}
	extract, err := migoextract.New(ssainfo, l.Writer)
	if err != nil {
		log.Fatal(err)
	}
	go extract.Run()

	select {
	case <-extract.Error:
		log.Fatal(err)
	case <-extract.Done:
		extract.Logger.Println("Analysis finished in", extract.Time)
	}

	extract.Env.MigoProg.CleanUp()
	if outfile != "" {
		f, err := os.Create(outfile)
		if err != nil {
			log.Fatal(err)
		}
		f.WriteString(extract.Env.MigoProg.String())
		defer f.Close()
	} else {
		os.Stdout.WriteString(extract.Env.MigoProg.String())
	}
}
