package parser

import (
	"bytes"
	"testing"
)

func TestParseWindowFunctions(t *testing.T) {
	src := []byte("SELECT id, ROW_NUMBER() OVER (PARTITION BY category ORDER BY score DESC) AS rn, RANK() OVER (PARTITION BY category ORDER BY score DESC) AS rnk, LAG(score, 1, 0) OVER (PARTITION BY category ORDER BY score) AS previous_score FROM documents")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.SelectStmts) != 1 || doc.SelectStmts[0].ProjectionsCount != 4 {
		t.Fatalf("unexpected SELECT AST: %#v", doc.SelectStmts)
	}
	if len(doc.FunctionExprs) != 3 || len(doc.WindowSpecs) != 3 {
		t.Fatalf("functions/windows=%d/%d", len(doc.FunctionExprs), len(doc.WindowSpecs))
	}
	for i, fn := range doc.FunctionExprs {
		if !fn.HasWindow || fn.WindowID != int32(i) {
			t.Fatalf("function %d window=%#v", i, fn)
		}
	}
	if doc.WindowSpecs[0].PartitionCount != 1 || doc.WindowSpecs[0].OrderBy.Kind != NodeKindIdentifier || !doc.WindowSpecs[0].IsDesc {
		t.Fatalf("row_number window=%#v", doc.WindowSpecs[0])
	}
	if doc.FunctionExprs[2].ArgsCount != 3 || doc.WindowSpecs[2].IsDesc {
		t.Fatalf("lag AST fn=%#v window=%#v", doc.FunctionExprs[2], doc.WindowSpecs[2])
	}
	if !bytes.Equal(src[doc.FunctionExprs[0].NameStart:doc.FunctionExprs[0].NameEnd], []byte("ROW_NUMBER")) {
		t.Fatalf("function name span=%q", src[doc.FunctionExprs[0].NameStart:doc.FunctionExprs[0].NameEnd])
	}
}

func TestParseWindowOrderListAndFrame(t *testing.T) {
	src := []byte("SELECT ROW_NUMBER() OVER (PARTITION BY category ORDER BY score DESC, id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS rn FROM documents")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.WindowSpecs) != 1 || len(doc.WindowOrders) != 2 {
		t.Fatalf("windows/orders=%d/%d", len(doc.WindowSpecs), len(doc.WindowOrders))
	}
	w := doc.WindowSpecs[0]
	if w.OrderCount != 2 || w.OrderBy.Kind != NodeKindIdentifier || !w.IsDesc {
		t.Fatalf("window order list=%#v", w)
	}
	if doc.WindowOrders[w.OrderStart].IsDesc != true || doc.WindowOrders[w.OrderStart+1].IsDesc {
		t.Fatalf("window order directions=%#v", doc.WindowOrders)
	}
	if !w.Frame.HasFrame || w.Frame.IsRange || w.Frame.Start.Kind != WindowFrameUnboundedPreceding || w.Frame.End.Kind != WindowFrameCurrentRow {
		t.Fatalf("window frame=%#v", w.Frame)
	}
}

func TestParseAggregateWindowFunction(t *testing.T) {
	src := []byte("SELECT SUM(score) OVER (PARTITION BY category ORDER BY score ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS running FROM documents")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.AggregateExprs) != 1 || !doc.AggregateExprs[0].HasWindow || doc.AggregateExprs[0].WindowID != 0 {
		t.Fatalf("aggregate window AST=%#v", doc.AggregateExprs)
	}
}

func TestParseNamedWindow(t *testing.T) {
	src := []byte("SELECT id, ROW_NUMBER() OVER ranked AS rn, SUM(score) OVER ranked AS running FROM documents WINDOW ranked AS (PARTITION BY category ORDER BY score DESC)")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.NamedWindows) != 1 || doc.SelectStmts[0].WindowDefsCount != 1 {
		t.Fatalf("named windows=%#v select=%#v", doc.NamedWindows, doc.SelectStmts[0])
	}
	if len(doc.FunctionExprs) != 1 || !doc.FunctionExprs[0].HasWindow || doc.FunctionExprs[0].WindowID != doc.NamedWindows[0].SpecID {
		t.Fatalf("named ranking window=%#v defs=%#v", doc.FunctionExprs, doc.NamedWindows)
	}
	if len(doc.AggregateExprs) != 1 || !doc.AggregateExprs[0].HasWindow || doc.AggregateExprs[0].WindowID != doc.NamedWindows[0].SpecID {
		t.Fatalf("named aggregate window=%#v defs=%#v", doc.AggregateExprs, doc.NamedWindows)
	}
}

func TestParseNumericRangeOffset(t *testing.T) {
	src := []byte("SELECT SUM(value) OVER (ORDER BY score RANGE BETWEEN 5 PRECEDING AND CURRENT ROW) AS running FROM documents")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.AggregateExprs) != 1 || len(doc.WindowSpecs) != 1 {
		t.Fatalf("aggregates/windows=%d/%d", len(doc.AggregateExprs), len(doc.WindowSpecs))
	}
	frame := doc.WindowSpecs[0].Frame
	if !frame.HasFrame || !frame.IsRange || frame.Start.Kind != WindowFramePreceding || frame.End.Kind != WindowFrameCurrentRow {
		t.Fatalf("range frame=%#v", frame)
	}
	if frame.Start.Expr.Kind != NodeKindNumber {
		t.Fatalf("range offset=%#v", frame.Start.Expr)
	}
}

func TestParseOrderedSetAggregates(t *testing.T) {
	src := []byte("SELECT PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY score), PERCENTILE_DISC(0.5) WITHIN GROUP (ORDER BY score DESC), MODE() WITHIN GROUP (ORDER BY category) FROM documents")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.AggregateExprs) != 3 {
		t.Fatalf("ordered-set aggregates=%d", len(doc.AggregateExprs))
	}
	if doc.AggregateExprs[0].Func != AggPercentileCont || !doc.AggregateExprs[0].OrderedSet || doc.AggregateExprs[0].OrderDesc {
		t.Fatalf("percentile_cont=%#v", doc.AggregateExprs[0])
	}
	if doc.AggregateExprs[1].Func != AggPercentileDisc || !doc.AggregateExprs[1].OrderedSet || !doc.AggregateExprs[1].OrderDesc {
		t.Fatalf("percentile_disc=%#v", doc.AggregateExprs[1])
	}
	if doc.AggregateExprs[2].Func != AggMode || !doc.AggregateExprs[2].OrderedSet || doc.AggregateExprs[2].OrderExpr.Kind != NodeKindIdentifier {
		t.Fatalf("mode=%#v", doc.AggregateExprs[2])
	}
}

func TestParseWindowNullOrderingAndDistributionFunctions(t *testing.T) {
	src := []byte("SELECT PERCENT_RANK() OVER (ORDER BY score DESC NULLS LAST), CUME_DIST() OVER (ORDER BY score), NTILE(2) OVER (ORDER BY score NULLS FIRST) FROM documents")
	var doc QueryDoc
	if err := Parse(src, &doc); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.FunctionExprs) != 3 || len(doc.WindowSpecs) != 3 {
		t.Fatalf("functions/windows=%d/%d", len(doc.FunctionExprs), len(doc.WindowSpecs))
	}
	if doc.WindowOrders[0].NullsOrder != WindowNullsLast || doc.WindowOrders[2].NullsOrder != WindowNullsFirst {
		t.Fatalf("NULL ordering=%#v", doc.WindowOrders)
	}
	if doc.FunctionExprs[0].ArgsCount != 0 || doc.FunctionExprs[1].ArgsCount != 0 || doc.FunctionExprs[2].ArgsCount != 1 {
		t.Fatalf("distribution function args=%#v", doc.FunctionExprs)
	}
}
