package fiberjoin_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/effect-go/effect-go/analysis/fiberjoin"
)

func Test(t *testing.T) { analysistest.Run(t, analysistest.TestData(), fiberjoin.Analyzer, "a") }
