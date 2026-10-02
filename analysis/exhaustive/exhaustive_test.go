package exhaustive_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/effect-go/effect-go/analysis/exhaustive"
)

func Test(t *testing.T) { analysistest.Run(t, analysistest.TestData(), exhaustive.Analyzer, "a") }
