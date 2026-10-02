// Command egovet runs the effect-go analyzers. Use it through go vet:
//
//	go vet -vettool=$(which egovet) ./...
package main

import (
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/effect-go/effect-go/analysis/ctxcheck"
	"github.com/effect-go/effect-go/analysis/exhaustive"
	"github.com/effect-go/effect-go/analysis/fiberjoin"
)

func main() { multichecker.Main(exhaustive.Analyzer, ctxcheck.Analyzer, fiberjoin.Analyzer) }
