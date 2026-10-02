package ctxcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/effect-go/effect-go/analysis/ctxcheck"
)

func Test(t *testing.T) { analysistest.Run(t, analysistest.TestData(), ctxcheck.Analyzer, "a") }
