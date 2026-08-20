package parser

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/xDarkicex/lexer"
)

// Parser holds the state for converting tokens into an AST.
type Parser struct {
	scanner             lexer.Scanner
	doc                 *QueryDoc
	src                 []byte // stored for zero-alloc number parsing from token offsets
	curr                lexer.Token
	next                lexer.Token
	functionArgsScratch []NodeRef
	arenaCursor         int
}

// Parse parses a SQL/PGQ byte stream into the provided QueryDoc.
// It resets the doc before parsing to guarantee zero allocations.
func Parse(src []byte, doc *QueryDoc) error {
	doc.Reset()
	p := &Parser{
		doc: doc,
		src: src,
	}
	p.scanner.Reset(src)
	// prime the pump
	p.advance()
	p.advance()

	for p.curr.Kind != lexer.KindEOF {
		switch p.curr.Kind {
		case lexer.KindExplain:
			doc.Explain = true
			p.advance()
			if p.curr.Kind == lexer.KindAnalyze {
				doc.ExplainAnalyze = true
				p.advance()
			}
			if p.curr.Kind != lexer.KindSelect && p.curr.Kind != lexer.KindWith {
				return fmt.Errorf("EXPLAIN requires a SELECT query")
			}
			doc.ExplainQueryStart = p.curr.Start
			stmtRef, err := p.parseSelectOrWith()
			if err != nil {
				return err
			}
			if p.curr.Kind == lexer.KindError && p.curr.Start < uint32(len(src)) && src[p.curr.Start] == ';' {
				p.advance()
			}
			doc.ExplainQueryEnd = uint32(len(src))
			for doc.ExplainQueryEnd > doc.ExplainQueryStart {
				last := src[doc.ExplainQueryEnd-1]
				if last == ';' || last == ' ' || last == '\t' || last == '\r' || last == '\n' {
					doc.ExplainQueryEnd--
					continue
				}
				break
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindWith:
			stmtRef, err := p.parseWithSelect()
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindSelect:
			stmtRef, err := p.parseSelectStmt()
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindMatch:
			stmtRef, err := p.parseCypherMatchStmt()
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindInsert:
			return p.dispatchInsertStmt()
		case lexer.KindUpdate:
			return p.parseUpdateStmt()
		case lexer.KindDelete:
			return p.parseDeleteStmt()
		case lexer.KindCreate:
			// Peek ahead to distinguish CREATE TABLE vs CREATE INDEX vs CREATE UNIQUE INDEX
			if p.next.Kind == lexer.KindUnique {
				// CREATE UNIQUE INDEX
				return p.parseCreateIndexStmt()
			}
			if p.next.Kind == lexer.KindTable {
				return p.parseCreateTableStmt()
			}
			if p.next.Kind == lexer.KindEdge {
				return p.parseCreateEdgeTypeStmt()
			}
			if p.next.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.next.Start:p.next.End], []byte("graph")) {
				return p.parseCreateGraphTableStmt()
			}
			if p.next.Kind == lexer.KindIndex {
				return p.parseCreateIndexStmt()
			}
			return fmt.Errorf("expected TABLE or INDEX after CREATE, got %v", p.next.Kind)
		case lexer.KindDrop:
			// Peek ahead to distinguish DROP TABLE vs DROP INDEX
			if p.next.Kind == lexer.KindIndex {
				return p.parseDropIndexStmt()
			}
			return p.parseDropTableStmt()
		case lexer.KindAlter:
			return p.parseAlterTableStmt()
		case lexer.KindBegin:
			p.advance()
			if p.curr.Kind == lexer.KindEpoch {
				p.advance()
				p.optionalTransactionKeyword()
				doc.TransactionStmts = append(doc.TransactionStmts, TransactionStmt{Kind: TransactionBeginEpoch})
			} else {
				p.optionalTransactionKeyword()
				doc.TransactionStmts = append(doc.TransactionStmts, TransactionStmt{Kind: TransactionBegin})
			}
		case lexer.KindStart:
			p.advance()
			if !p.optionalTransactionKeyword() {
				return fmt.Errorf("expected TRANSACTION after START")
			}
			doc.TransactionStmts = append(doc.TransactionStmts, TransactionStmt{Kind: TransactionBegin})
		case lexer.KindCommit:
			p.advance()
			p.optionalTransactionKeyword()
			doc.TransactionStmts = append(doc.TransactionStmts, TransactionStmt{Kind: TransactionCommit})
		case lexer.KindRollback:
			p.advance()
			// Check for ROLLBACK TO SAVEPOINT <name> or ROLLBACK TO <name>
			if p.curr.Kind == lexer.KindTo {
				p.advance()
				// Optional SAVEPOINT keyword
				if p.curr.Kind == lexer.KindSavepoint {
					p.advance()
				}
				if p.curr.Kind != lexer.KindIdentifier {
					return fmt.Errorf("expected savepoint name after ROLLBACK TO")
				}
				name := string(p.src[p.curr.Start:p.curr.End])
				p.advance()
				doc.TransactionStmts = append(doc.TransactionStmts, TransactionStmt{Kind: TransactionRollbackToSavepoint, SavepointName: name})
			} else {
				p.optionalTransactionKeyword()
				doc.TransactionStmts = append(doc.TransactionStmts, TransactionStmt{Kind: TransactionRollback})
			}
		case lexer.KindSavepoint:
			p.advance()
			if p.curr.Kind != lexer.KindIdentifier {
				return fmt.Errorf("expected savepoint name after SAVEPOINT")
			}
			name := string(p.src[p.curr.Start:p.curr.End])
			p.advance()
			doc.TransactionStmts = append(doc.TransactionStmts, TransactionStmt{Kind: TransactionSavepoint, SavepointName: name})
		case lexer.KindCompute:
			stmtRef, err := p.parseComputeLeidenStmt()
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindPrepare:
			stmtRef, err := p.parsePrepareStmt()
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindExecute:
			stmtRef, err := p.parseExecuteStmt()
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindMerge:
			stmtRef, err := p.parseMergeStmt()
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindSet:
			stmtRef, err := p.parseSessionSettingStmt(false)
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindReset:
			stmtRef, err := p.parseSessionSettingStmt(true)
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindRelease:
			p.advance()
			// PostgreSQL accepts both RELEASE SAVEPOINT name and the shorter
			// RELEASE name form used by psycopg transaction contexts.
			if p.curr.Kind == lexer.KindSavepoint {
				p.advance()
			}
			if p.curr.Kind != lexer.KindIdentifier {
				return fmt.Errorf("expected savepoint name after RELEASE SAVEPOINT")
			}
			name := string(p.src[p.curr.Start:p.curr.End])
			p.advance()
			doc.TransactionStmts = append(doc.TransactionStmts, TransactionStmt{Kind: TransactionReleaseSavepoint, SavepointName: name})
		default:
			// If we hit unexpected tokens at root, we break or error.
			// For this subset parser, just break.
			return fmt.Errorf("unexpected token at root: %v", p.curr.Kind)
		}
	}
	return nil
}

func (p *Parser) parseSelectOrWith() (NodeRef, error) {
	if p.curr.Kind == lexer.KindWith {
		return p.parseWithSelect()
	}
	return p.parseSelectStmt()
}

// parseCypherMatchStmt lowers the native Cypher surface into the existing
// SelectStmt/GraphTable representation. The graph executor therefore remains
// shared with SQL JOIN MATCH and GRAPH_TABLE rather than creating a second
// traversal implementation.
func (p *Parser) parseCypherMatchStmt() (NodeRef, error) {
	sourceStart := p.curr.Start
	p.advance() // MATCH

	var matchRef NodeRef
	var pathAliasStart, pathAliasEnd uint32
	if p.curr.Kind == lexer.KindIdentifier && p.next.Kind == lexer.KindEquals {
		pathAliasStart, pathAliasEnd = p.curr.Start, p.curr.End
		p.advance() // =
		p.advance() // pattern or shortestPath
	}
	if p.curr.Kind == lexer.KindShortestPath {
		p.advance()
		if err := p.expect(lexer.KindLeftParen); err != nil {
			return NodeRef{}, fmt.Errorf("shortestPath: %w", err)
		}
		var err error
		matchRef, err = p.parseMatchPath()
		if err != nil {
			return NodeRef{}, err
		}
		if err := p.expect(lexer.KindRightParen); err != nil {
			return NodeRef{}, fmt.Errorf("shortestPath: %w", err)
		}
		p.doc.MatchPaths[matchRef.ID].Shortest = true
	} else {
		var err error
		matchRef, err = p.parseMatchPath()
		if err != nil {
			return NodeRef{}, err
		}
		if matchRef.Kind != NodeKindMatchPath {
			return NodeRef{}, fmt.Errorf("invalid MATCH pattern")
		}
	}
	if pathAliasEnd > pathAliasStart {
		p.doc.MatchPaths[matchRef.ID].PathAlias = pathAliasStart
		p.doc.MatchPaths[matchRef.ID].PathAliasEnd = pathAliasEnd
	}

	stmt := SelectStmt{
		ID:          int32(len(p.doc.SelectStmts)),
		SourceStart: sourceStart,
		Limit:       -1,
		Offset:      -1,
	}
	stmt.Joins = p.doc.reusableJoinArena(p.arenaCursor)
	stmt.GroupBy = p.doc.reusableGroupByArena(p.arenaCursor)
	stmt.OrderTerms = p.doc.reusableOrderTermsArena(p.arenaCursor)
	p.arenaCursor++
	gt := GraphTable{ID: int32(len(p.doc.GraphTables)), MatchPath: matchRef}
	p.doc.GraphTables = append(p.doc.GraphTables, gt)
	stmt.FromTable = NodeRef{Kind: NodeKindGraphTable, ID: gt.ID}

	// Cypher places WHERE between the pattern and RETURN.
	if p.curr.Kind == lexer.KindWhere {
		p.advance()
		where, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		stmt.WhereExpr = where
	}
	if p.curr.Kind == lexer.KindDetach || p.curr.Kind == lexer.KindDelete {
		return p.parseCypherDelete(matchRef, stmt.WhereExpr)
	}

	// A native Cypher pipe keeps the same graph source and stores each WITH
	// boundary on the SELECT statement. The executor consumes these clauses
	// before evaluating the final RETURN projection.
	pipeStart := int32(len(p.doc.WithClauses))
	for p.curr.Kind == lexer.KindWith {
		clause, err := p.parseCypherWithClause()
		if err != nil {
			return NodeRef{}, err
		}
		if p.curr.Kind == lexer.KindMatch {
			p.advance()
			matchPath, matchErr := p.parseMatchPath()
			if matchErr != nil {
				return NodeRef{}, fmt.Errorf("MATCH after WITH: %w", matchErr)
			}
			clause.MatchPath = matchPath
			if p.curr.Kind == lexer.KindWhere {
				p.advance()
				where, whereErr := p.parseExpr(0)
				if whereErr != nil {
					return NodeRef{}, fmt.Errorf("MATCH after WITH WHERE: %w", whereErr)
				}
				clause.MatchWhere = where
			}
		}
		p.doc.WithClauses = append(p.doc.WithClauses, clause)
	}
	stmt.PipeWithStart = pipeStart
	stmt.PipeWithCount = int32(len(p.doc.WithClauses)) - pipeStart
	if p.curr.Kind != lexer.KindReturn {
		return NodeRef{}, fmt.Errorf("MATCH requires RETURN")
	}
	p.advance()
	local := make([]Projection, 0, 4)
	for p.curr.Kind != lexer.KindEOF && p.curr.Kind != lexer.KindOrder &&
		p.curr.Kind != lexer.KindLimit && p.curr.Kind != lexer.KindOffset {
		proj, err := p.parseProjection()
		if err != nil {
			return NodeRef{}, err
		}
		local = append(local, proj)
		stmt.ProjectionsCount++
		if p.curr.Kind != lexer.KindComma {
			break
		}
		p.advance()
	}
	if len(local) == 0 {
		return NodeRef{}, fmt.Errorf("RETURN requires at least one projection")
	}

	if p.curr.Kind == lexer.KindOrder {
		p.advance()
		if err := p.expect(lexer.KindBy); err != nil {
			return NodeRef{}, err
		}
		for {
			expr, err := p.parseExpr(0)
			if err != nil {
				return NodeRef{}, err
			}
			term := OrderTerm{Expr: expr}
			if p.curr.Kind == lexer.KindDesc {
				term.IsDesc = true
				p.advance()
			} else if p.curr.Kind == lexer.KindAsc {
				p.advance()
			}
			stmt.OrderTerms = append(stmt.OrderTerms, term)
			if len(stmt.OrderTerms) == 1 {
				stmt.OrderBy, stmt.IsDesc = term.Expr, term.IsDesc
			}
			if p.curr.Kind != lexer.KindComma {
				break
			}
			p.advance()
		}
	}
	for p.curr.Kind == lexer.KindLimit || p.curr.Kind == lexer.KindOffset {
		isOffset := p.curr.Kind == lexer.KindOffset
		p.advance()
		if p.curr.Kind == lexer.KindParam {
			ref, err := p.parseExpr(0)
			if err != nil {
				return NodeRef{}, err
			}
			if isOffset {
				stmt.OffsetExpr = ref
			} else {
				stmt.LimitExpr = ref
			}
			continue
		}
		if p.curr.Kind != lexer.KindNumber {
			return NodeRef{}, fmt.Errorf("expected number after graph %s", map[bool]string{true: "OFFSET", false: "LIMIT"}[isOffset])
		}
		n := Number{ID: int32(len(p.doc.Numbers)), Start: p.curr.Start, End: p.curr.End}
		p.doc.Numbers = append(p.doc.Numbers, n)
		if isOffset {
			stmt.Offset = n.ID
		} else {
			stmt.Limit = n.ID
		}
		p.advance()
	}
	stmt.ProjectionsStart = int32(len(p.doc.Projections))
	for i := range local {
		local[i].ID = int32(len(p.doc.Projections))
		p.doc.Projections = append(p.doc.Projections, local[i])
	}
	stmt.SourceEnd = p.curr.Start
	if stmt.SourceEnd == 0 || stmt.SourceEnd > uint32(len(p.src)) {
		stmt.SourceEnd = uint32(len(p.src))
	}
	p.doc.SelectStmts = append(p.doc.SelectStmts, stmt)
	p.doc.joinArenas[p.arenaCursor-1] = stmt.Joins
	p.doc.groupByArenas[p.arenaCursor-1] = stmt.GroupBy
	p.doc.orderTermsArenas[p.arenaCursor-1] = stmt.OrderTerms
	return NodeRef{Kind: NodeKindSelectStmt, ID: stmt.ID}, nil
}

func (p *Parser) parseCypherDelete(matchRef NodeRef, where NodeRef) (NodeRef, error) {
	detach := false
	if p.curr.Kind == lexer.KindDetach {
		detach = true
		p.advance()
	}
	if p.curr.Kind != lexer.KindDelete {
		return NodeRef{}, fmt.Errorf("expected DELETE after MATCH")
	}
	p.advance()
	targets := make([]NodeRef, 0, 2)
	for {
		if p.curr.Kind != lexer.KindIdentifier && p.curr.Kind != lexer.KindKey && p.curr.Kind != lexer.KindOptional {
			return NodeRef{}, fmt.Errorf("expected graph alias after DELETE, got %v", p.curr.Kind)
		}
		id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		targets = append(targets, NodeRef{Kind: NodeKindIdentifier, ID: id.ID})
		p.advance()
		if p.curr.Kind != lexer.KindComma {
			break
		}
		p.advance()
	}
	stmt := DeleteStmt{
		Cypher: true, Detach: detach, MatchPath: matchRef,
		Targets: targets, WhereExpr: where,
	}
	id := int32(len(p.doc.DeleteStmts))
	p.doc.DeleteStmts = append(p.doc.DeleteStmts, stmt)
	return NodeRef{Kind: NodeKindDeleteStmt, ID: id}, nil
}

func (p *Parser) parseCypherWithClause() (WithClause, error) {
	p.advance() // WITH
	clause := WithClause{ID: int32(len(p.doc.WithClauses))}
	if p.curr.Kind == lexer.KindDistinct {
		clause.Distinct = true
		p.advance()
	}
	for {
		if p.curr.Kind == lexer.KindWhere || p.curr.Kind == lexer.KindOrder ||
			p.curr.Kind == lexer.KindSkip || p.curr.Kind == lexer.KindLimit ||
			p.curr.Kind == lexer.KindWith || p.curr.Kind == lexer.KindMatch ||
			p.curr.Kind == lexer.KindReturn || p.curr.Kind == lexer.KindEOF {
			break
		}
		projection, err := p.parseProjection()
		if err != nil {
			return WithClause{}, fmt.Errorf("WITH projection: %w", err)
		}
		clause.Projections = append(clause.Projections, projection)
		if p.curr.Kind != lexer.KindComma {
			break
		}
		p.advance()
	}
	if len(clause.Projections) == 0 {
		return WithClause{}, fmt.Errorf("WITH requires at least one projection")
	}
	if p.curr.Kind == lexer.KindWhere {
		p.advance()
		where, err := p.parseExpr(0)
		if err != nil {
			return WithClause{}, err
		}
		clause.Where = where
	}
	if p.curr.Kind == lexer.KindOrder {
		p.advance()
		if err := p.expect(lexer.KindBy); err != nil {
			return WithClause{}, err
		}
		for {
			expr, err := p.parseExpr(0)
			if err != nil {
				return WithClause{}, err
			}
			term := OrderTerm{Expr: expr}
			if p.curr.Kind == lexer.KindDesc {
				term.IsDesc = true
				p.advance()
			} else if p.curr.Kind == lexer.KindAsc {
				p.advance()
			}
			clause.OrderTerms = append(clause.OrderTerms, term)
			if len(clause.OrderTerms) == 1 {
				clause.OrderBy = term.Expr
			}
			if p.curr.Kind != lexer.KindComma {
				break
			}
			p.advance()
		}
	}
	for p.curr.Kind == lexer.KindSkip || p.curr.Kind == lexer.KindLimit {
		isSkip := p.curr.Kind == lexer.KindSkip
		p.advance()
		expr, err := p.parseExpr(0)
		if err != nil {
			return WithClause{}, fmt.Errorf("WITH %s: %w", map[bool]string{true: "SKIP", false: "LIMIT"}[isSkip], err)
		}
		if isSkip {
			clause.Skip = expr
		} else {
			clause.Limit = expr
		}
	}
	return clause, nil
}

// parseSessionSettingStmt parses the session-local controls supported by the
// runtime. Values remain typed AST nodes; the session binder owns conversion.
func (p *Parser) parseSessionSettingStmt(reset bool) (NodeRef, error) {
	p.advance() // SET or RESET
	local := false
	if !reset && p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("local")) {
		local = true
		p.advance()
	}
	if p.curr.Kind != lexer.KindIdentifier {
		verb := "SET"
		if reset {
			verb = "RESET"
		}
		return NodeRef{}, fmt.Errorf("expected session setting name after %s", verb)
	}
	nameStart, nameEnd := p.curr.Start, p.curr.End
	name := p.src[nameStart:nameEnd]
	var kind SessionSetting
	switch {
	case bytes.EqualFold(name, []byte("statement_timeout")):
		kind = SessionSettingStatementTimeout
	case bytes.EqualFold(name, []byte("max_recursion_depth")):
		kind = SessionSettingMaxRecursionDepth
	case bytes.EqualFold(name, []byte("enable_seqscan")):
		kind = SessionSettingEnableSeqScan
	default:
		return NodeRef{}, fmt.Errorf("unsupported session setting %q", name)
	}
	p.advance()
	stmt := SessionSettingStmt{ID: int32(len(p.doc.SessionSettingStmts)), Kind: kind, NameStart: nameStart, NameEnd: nameEnd, Reset: reset, Local: local}
	if !reset {
		if p.curr.Kind == lexer.KindEquals || p.curr.Kind == lexer.KindTo {
			p.advance()
		} else {
			return NodeRef{}, fmt.Errorf("expected = or TO after session setting %q", name)
		}
		value, err := p.parseSessionSettingValue()
		if err != nil {
			return NodeRef{}, err
		}
		stmt.Value = value
	}
	if p.curr.Kind == lexer.KindError && p.curr.Start < uint32(len(p.src)) && p.src[p.curr.Start] == ';' {
		p.advance()
	}
	if p.curr.Kind != lexer.KindEOF {
		return NodeRef{}, fmt.Errorf("unexpected token after session setting: %v", p.curr.Kind)
	}
	p.doc.SessionSettingStmts = append(p.doc.SessionSettingStmts, stmt)
	return NodeRef{Kind: NodeKindSessionSettingStmt, ID: stmt.ID}, nil
}

func (p *Parser) parseSessionSettingValue() (NodeRef, error) {
	switch p.curr.Kind {
	case lexer.KindString, lexer.KindEscapeString:
		sl := StringLiteral{ID: int32(len(p.doc.Strings)), Start: p.curr.Start, End: p.curr.End, Escape: p.curr.Kind == lexer.KindEscapeString}
		p.doc.Strings = append(p.doc.Strings, sl)
		p.advance()
		return NodeRef{Kind: NodeKindString, ID: sl.ID}, nil
	case lexer.KindNumber:
		n := Number{ID: int32(len(p.doc.Numbers)), Start: p.curr.Start, End: p.curr.End}
		p.doc.Numbers = append(p.doc.Numbers, n)
		p.advance()
		return NodeRef{Kind: NodeKindNumber, ID: n.ID}, nil
	case lexer.KindIdentifier, lexer.KindNull, lexer.KindOn, lexer.KindDefault:
		id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
		if p.curr.Kind == lexer.KindNull {
			id.ResolvedKind = ResolvedKindLiteral
		}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		p.advance()
		return NodeRef{Kind: NodeKindIdentifier, ID: id.ID}, nil
	default:
		return NodeRef{}, fmt.Errorf("expected a session setting value, got %v", p.curr.Kind)
	}
}

func (p *Parser) parsePrepareStmt() (NodeRef, error) {
	p.advance() // PREPARE
	if p.curr.Kind != lexer.KindIdentifier {
		return NodeRef{}, fmt.Errorf("expected prepared statement name after PREPARE")
	}
	stmt := PrepareStmt{ID: int32(len(p.doc.PrepareStmts)), NameStart: p.curr.Start, NameEnd: p.curr.End}
	p.advance()
	if p.curr.Kind == lexer.KindLeftParen {
		// Optional PostgreSQL parameter type list. Types are retained as part
		// of the prepared SQL contract but do not affect execution typing yet.
		depth := 1
		p.advance()
		for depth > 0 && p.curr.Kind != lexer.KindEOF {
			if p.curr.Kind == lexer.KindLeftParen {
				depth++
			} else if p.curr.Kind == lexer.KindRightParen {
				depth--
			}
			p.advance()
		}
		if depth != 0 {
			return NodeRef{}, fmt.Errorf("unterminated PREPARE parameter type list")
		}
	}
	if p.curr.Kind != lexer.KindAs {
		return NodeRef{}, fmt.Errorf("expected AS after PREPARE name")
	}
	p.advance()
	if p.curr.Kind == lexer.KindEOF || p.curr.Kind == lexer.KindError {
		return NodeRef{}, fmt.Errorf("expected query after PREPARE ... AS")
	}
	stmt.QueryStart = p.curr.Start
	stmt.QueryEnd = uint32(len(p.src))
	for p.curr.Kind != lexer.KindEOF {
		if p.curr.Kind == lexer.KindError {
			if p.curr.Start < uint32(len(p.src)) && p.src[p.curr.Start] == ';' {
				stmt.QueryEnd = p.curr.Start
				p.advance()
				break
			}
			return NodeRef{}, fmt.Errorf("invalid token in PREPARE body")
		}
		p.advance()
	}
	p.doc.PrepareStmts = append(p.doc.PrepareStmts, stmt)
	return NodeRef{Kind: NodeKindPrepareStmt, ID: stmt.ID}, nil
}

func (p *Parser) parseExecuteStmt() (NodeRef, error) {
	p.advance() // EXECUTE
	if p.curr.Kind != lexer.KindIdentifier {
		return NodeRef{}, fmt.Errorf("expected prepared statement name after EXECUTE")
	}
	stmt := ExecuteStmt{ID: int32(len(p.doc.ExecuteStmts)), NameStart: p.curr.Start, NameEnd: p.curr.End, ArgsStart: int32(len(p.doc.ExecuteArgs))}
	p.advance()
	if p.curr.Kind == lexer.KindLeftParen {
		p.advance()
		if p.curr.Kind == lexer.KindRightParen {
			p.advance()
		} else {
			for {
				arg, err := p.parseExpr(0)
				if err != nil {
					return NodeRef{}, fmt.Errorf("EXECUTE argument: %w", err)
				}
				p.doc.ExecuteArgs = append(p.doc.ExecuteArgs, arg)
				stmt.ArgsCount++
				if p.curr.Kind != lexer.KindComma {
					break
				}
				p.advance()
			}
			if err := p.expect(lexer.KindRightParen); err != nil {
				return NodeRef{}, err
			}
		}
	}
	if p.curr.Kind == lexer.KindError && p.curr.Start < uint32(len(p.src)) && p.src[p.curr.Start] == ';' {
		p.advance()
	}
	p.doc.ExecuteStmts = append(p.doc.ExecuteStmts, stmt)
	return NodeRef{Kind: NodeKindExecuteStmt, ID: stmt.ID}, nil
}

// parseMergeStmt parses the Graphiti-compatible graph upsert surface:
// MERGE (a:Person {uuid: $uuid})-[:KNOWS]->(b)
//
//	ON CREATE SET a.created_at = $ts
//	ON MATCH SET a.updated_at = $ts
//
// The graph executor owns conflict resolution and atomic publication.
func (p *Parser) parseMergeStmt() (NodeRef, error) {
	p.advance() // MERGE
	pathAliasStart, pathAliasEnd := uint32(0), uint32(0)
	if p.curr.Kind == lexer.KindIdentifier && p.next.Kind == lexer.KindEquals {
		pathAliasStart, pathAliasEnd = p.curr.Start, p.curr.End
		p.advance()
		p.advance()
	}
	if p.curr.Kind != lexer.KindLeftParen {
		return NodeRef{}, fmt.Errorf("MERGE requires a graph pattern")
	}
	matchRef, err := p.parseMatchPath()
	if err != nil {
		return NodeRef{}, fmt.Errorf("MERGE pattern: %w", err)
	}
	if pathAliasEnd > pathAliasStart && matchRef.ID >= 0 && int(matchRef.ID) < len(p.doc.MatchPaths) {
		p.doc.MatchPaths[matchRef.ID].PathAlias = pathAliasStart
		p.doc.MatchPaths[matchRef.ID].PathAliasEnd = pathAliasEnd
	}
	stmt := MergeStmt{ID: int32(len(p.doc.MergeStmts)), MatchPath: matchRef}
	if p.curr.Kind == lexer.KindSet {
		p.advance()
		start := int32(len(p.doc.MergeAssignments))
		if err := p.parseMergeAssignmentList(); err != nil {
			return NodeRef{}, err
		}
		stmt.UniversalSetStart = start
		stmt.UniversalSetCount = int32(len(p.doc.MergeAssignments)) - start
	}
	for p.curr.Kind == lexer.KindOn {
		p.advance()
		section := p.curr.Kind
		if section != lexer.KindMatch && section != lexer.KindCreate {
			return NodeRef{}, fmt.Errorf("expected MATCH or CREATE after ON")
		}
		p.advance()
		if p.curr.Kind != lexer.KindSet {
			return NodeRef{}, fmt.Errorf("expected SET after ON")
		}
		p.advance()
		start := int32(len(p.doc.MergeAssignments))
		if err := p.parseMergeAssignmentList(); err != nil {
			return NodeRef{}, err
		}
		count := int32(len(p.doc.MergeAssignments)) - start
		if section == lexer.KindCreate {
			stmt.OnCreateStart, stmt.OnCreateCount = start, count
		} else {
			stmt.OnMatchStart, stmt.OnMatchCount = start, count
		}
	}
	if p.curr.Kind == lexer.KindReturning {
		if err := p.parseReturning(&stmt.Returning, &stmt.ReturningStar); err != nil {
			return NodeRef{}, err
		}
	} else if p.curr.Kind == lexer.KindReturn {
		p.advance()
		if p.curr.Kind == lexer.KindAsterisk {
			stmt.ReturningStar = true
			p.advance()
		} else {
			for {
				projection, projectionErr := p.parseProjection()
				if projectionErr != nil {
					return NodeRef{}, projectionErr
				}
				stmt.ReturningProjections = append(stmt.ReturningProjections, projection)
				stmt.Returning = append(stmt.Returning, projection.Expr)
				if p.curr.Kind != lexer.KindComma {
					break
				}
				p.advance()
			}
		}
	}
	p.doc.MergeStmts = append(p.doc.MergeStmts, stmt)
	return NodeRef{Kind: NodeKindMergeStmt, ID: stmt.ID}, nil
}

func (p *Parser) parseMergeAssignmentList() error {
	for {
		// Parse only the assignment target. Using precedence zero would consume
		// the following '=' as part of the expression, leaving no delimiter for
		// MERGE's SET grammar.
		column, err := p.parseExpr(operatorPrecedence(lexer.KindEquals))
		if err != nil {
			return fmt.Errorf("MERGE assignment target: %w", err)
		}
		if column.Kind != NodeKindIdentifier || p.curr.Kind != lexer.KindEquals {
			return fmt.Errorf("MERGE SET requires alias.column = expression")
		}
		p.advance()
		value, err := p.parseExpr(0)
		if err != nil {
			return fmt.Errorf("MERGE assignment value: %w", err)
		}
		p.doc.MergeAssignments = append(p.doc.MergeAssignments, MergeAssignment{Column: column, Value: value})
		if p.curr.Kind != lexer.KindComma {
			return nil
		}
		p.advance()
	}
}

func (p *Parser) advance() {
	p.curr = p.next
	for {
		tok, ok := p.scanner.Next()
		if !ok || tok.Kind == lexer.KindEOF {
			p.next = lexer.Token{Kind: lexer.KindEOF}
			break
		}
		if tok.Kind != lexer.KindWhitespace {
			p.next = tok
			break
		}
	}
}

// optionalTransactionKeyword consumes the optional TRANSACTION or WORK word
// accepted after BEGIN, START, COMMIT, and ROLLBACK. Both words are currently
// lexed as identifiers, so this helper keeps transaction grammar centralized.
func (p *Parser) optionalTransactionKeyword() bool {
	if p.curr.Kind != lexer.KindIdentifier {
		return false
	}
	word := p.src[p.curr.Start:p.curr.End]
	if bytes.EqualFold(word, []byte("transaction")) || bytes.EqualFold(word, []byte("work")) {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) expect(kind lexer.Kind) error {
	if p.curr.Kind != kind {
		return fmt.Errorf("expected %v, got %v", kind, p.curr.Kind)
	}
	p.advance()
	return nil
}

// parseWithSelect parses WITH [RECURSIVE] cte AS (SELECT ... | COMPUTE LEIDEN ...)
// [, cte AS (...)] SELECT ... . CTE bodies are retained as ordinary AST
// SELECTs; execution resolves their names through a query-local virtual
// relation environment.
func (p *Parser) parseWithSelect() (NodeRef, error) {
	p.advance() // consume WITH
	recursive := false
	if p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("recursive")) {
		recursive = true
		p.advance()
	}
	cteStart := int32(len(p.doc.CTEs))
	for {
		if p.curr.Kind != lexer.KindIdentifier {
			return NodeRef{}, fmt.Errorf("expected CTE name after WITH, got %v", p.curr.Kind)
		}
		cteName := string(p.src[p.curr.Start:p.curr.End])
		cteNameStart := p.curr.Start
		cteNameEnd := p.curr.End
		cteNameLower := toLower(cteName)
		for _, existing := range p.doc.CTEs[cteStart:] {
			existingName := string(p.src[existing.NameStart:existing.NameEnd])
			if toLower(existingName) == cteNameLower {
				return NodeRef{}, fmt.Errorf("duplicate CTE name %q", cteName)
			}
		}
		p.advance()
		if p.curr.Kind != lexer.KindAs {
			return NodeRef{}, fmt.Errorf("expected AS after CTE name %q, got %v", cteName, p.curr.Kind)
		}
		p.advance()
		if err := p.expect(lexer.KindLeftParen); err != nil {
			return NodeRef{}, fmt.Errorf("expected '(' after AS: %w", err)
		}
		var body NodeRef
		if p.curr.Kind == lexer.KindCompute {
			if recursive {
				return NodeRef{}, fmt.Errorf("recursive COMPUTE LEIDEN CTEs are not supported")
			}
			leidenStmt, err := p.parseComputeLeidenStmtBody()
			if err != nil {
				return NodeRef{}, fmt.Errorf("in CTE body: %w", err)
			}
			p.doc.ComputeLeidenStmts = append(p.doc.ComputeLeidenStmts, leidenStmt)
			body = NodeRef{Kind: NodeKindComputeLeidenStmt, ID: leidenStmt.ID}
		} else if p.curr.Kind == lexer.KindSelect {
			selectRef, err := p.parseSelectStmt()
			if err != nil {
				return NodeRef{}, fmt.Errorf("in CTE body: %w", err)
			}
			body = selectRef
		} else {
			return NodeRef{}, fmt.Errorf("expected SELECT or COMPUTE LEIDEN in CTE body, got %v", p.curr.Kind)
		}
		if err := p.expect(lexer.KindRightParen); err != nil {
			return NodeRef{}, fmt.Errorf("expected ')' after CTE body: %w", err)
		}
		cteID := int32(len(p.doc.CTEs))
		p.doc.CTEs = append(p.doc.CTEs, CTE{ID: cteID, NameStart: cteNameStart, NameEnd: cteNameEnd, Body: body, Recursive: recursive})
		if p.curr.Kind != lexer.KindComma {
			break
		}
		p.advance()
	}
	if p.curr.Kind != lexer.KindSelect {
		return NodeRef{}, fmt.Errorf("expected SELECT after CTE, got %v", p.curr.Kind)
	}
	selectRef, err := p.parseSelectStmt()
	if err != nil {
		return NodeRef{}, fmt.Errorf("in outer SELECT: %w", err)
	}
	if selectRef.Kind == NodeKindSelectStmt {
		stmt := &p.doc.SelectStmts[selectRef.ID]
		stmt.CTEsStart = cteStart
		stmt.CTEsCount = int32(len(p.doc.CTEs)) - cteStart
	}

	return selectRef, nil
}

func (p *Parser) parseSelectStmt() (NodeRef, error) {
	sourceStart := p.curr.Start
	p.advance() // consume SELECT

	// Arena identity follows parse nesting order, while SelectStmt.ID follows
	// the final statement publication order (nested SELECTs are published
	// before their containing SELECT). Keeping them separate prevents nested
	// parsing from aliasing a parent's reusable clause backing arrays.
	arenaID := p.arenaCursor
	p.arenaCursor++
	stmt := SelectStmt{
		ID:              int32(len(p.doc.SelectStmts)),
		SourceStart:     sourceStart,
		Limit:           -1,
		Offset:          -1,
		WindowDefsStart: int32(len(p.doc.NamedWindows)),
	}
	stmt.Joins = p.doc.reusableJoinArena(arenaID)
	stmt.GroupBy = p.doc.reusableGroupByArena(arenaID)
	stmt.OrderTerms = p.doc.reusableOrderTermsArena(arenaID)
	// Keep this SELECT's projection refs local while parsing. Nested SELECTs
	// can append their own projections during parseExpr; publishing the outer
	// slice only after the full statement is parsed keeps every SelectStmt's
	// ProjectionsStart/Count range contiguous in the SoA arena.
	localProjections := make([]Projection, 0, 4)
	if p.curr.Kind == lexer.KindDistinct {
		stmt.Distinct = true
		p.advance()
	}

	// Parse projections
	for p.curr.Kind != lexer.KindEOF && p.curr.Kind != lexer.KindFrom {
		proj, err := p.parseProjection()
		if err != nil {
			return NodeRef{}, err
		}
		localProjections = append(localProjections, proj)
		stmt.ProjectionsCount++

		if p.curr.Kind == lexer.KindComma {
			p.advance()
		} else {
			break
		}
	}

	if p.curr.Kind == lexer.KindFrom {
		p.advance()
		fromRef, err := p.parseTableExpr()
		if err != nil {
			return NodeRef{}, err
		}
		stmt.FromTable = fromRef
	} else if p.curr.Kind == lexer.KindMatch {
		// Standalone MATCH without GRAPH_TABLE or FROM wrapper.
		// Parse the match path directly; the optimizer creates an
		// implicit graph source from the first graph-enabled collection.
		matchRef := p.parseMatchPathNode()
		gt := GraphTable{ID: int32(len(p.doc.GraphTables)), MatchPath: matchRef}
		p.doc.GraphTables = append(p.doc.GraphTables, gt)
		stmt.FromTable = NodeRef{Kind: NodeKindGraphTable, ID: gt.ID}
	}

	// Parse JOIN clauses
	for p.curr.Kind == lexer.KindJoin ||
		p.curr.Kind == lexer.KindOptional ||
		p.curr.Kind == lexer.KindLeft ||
		p.curr.Kind == lexer.KindRight ||
		p.curr.Kind == lexer.KindInner ||
		p.curr.Kind == lexer.KindFull ||
		p.curr.Kind == lexer.KindCross ||
		p.curr.Kind == lexer.KindOuter {
		jc, err := p.parseJoinClause()
		if err != nil {
			return NodeRef{}, err
		}
		stmt.Joins = append(stmt.Joins, jc)
	}

	if p.curr.Kind == lexer.KindWhere {
		p.advance()
		whereRef, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		stmt.WhereExpr = whereRef
	}

	// GROUP BY col1, col2, ...
	if p.curr.Kind == lexer.KindGroup {
		p.advance()
		if err := p.expect(lexer.KindBy); err != nil {
			return NodeRef{}, err
		}
		for p.curr.Kind != lexer.KindEOF &&
			p.curr.Kind != lexer.KindHaving &&
			p.curr.Kind != lexer.KindOrder &&
			p.curr.Kind != lexer.KindLimit &&
			p.curr.Kind != lexer.KindOffset {
			expr, err := p.parseExpr(0)
			if err != nil {
				return NodeRef{}, err
			}
			stmt.GroupBy = append(stmt.GroupBy, expr)
			if p.curr.Kind == lexer.KindComma {
				p.advance()
			} else {
				break
			}
		}
	}

	// HAVING condition
	if p.curr.Kind == lexer.KindHaving {
		p.advance()
		havingRef, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		stmt.HavingExpr = havingRef
	}

	// WINDOW name AS (...) definitions are scoped to this SELECT. Definitions
	// are resolved against OVER name references after the complete SELECT has
	// been parsed, because SQL places WINDOW after HAVING and before ORDER BY.
	if p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("window")) {
		p.advance()
		for {
			if !isIdentifierLike(p.curr.Kind) {
				return NodeRef{}, fmt.Errorf("expected named window name")
			}
			nameStart, nameEnd := p.curr.Start, p.curr.End
			for i := stmt.WindowDefsStart; i < int32(len(p.doc.NamedWindows)); i++ {
				old := p.doc.NamedWindows[i]
				if bytes.EqualFold(p.src[old.NameStart:old.NameEnd], p.src[nameStart:nameEnd]) {
					return NodeRef{}, fmt.Errorf("duplicate named window %q", string(p.src[nameStart:nameEnd]))
				}
			}
			p.advance()
			if err := p.expect(lexer.KindAs); err != nil {
				return NodeRef{}, fmt.Errorf("named window %q: %w", string(p.src[nameStart:nameEnd]), err)
			}
			spec, err := p.parseWindowSpecBody()
			if err != nil {
				return NodeRef{}, fmt.Errorf("named window %q: %w", string(p.src[nameStart:nameEnd]), err)
			}
			p.doc.NamedWindows = append(p.doc.NamedWindows, NamedWindow{ID: int32(len(p.doc.NamedWindows)), NameStart: nameStart, NameEnd: nameEnd, SpecID: spec.ID})
			stmt.WindowDefsCount++
			if p.curr.Kind != lexer.KindComma {
				break
			}
			p.advance()
		}
	}

	if p.curr.Kind == lexer.KindOrder {
		p.advance()
		if err := p.expect(lexer.KindBy); err != nil {
			return NodeRef{}, err
		}
		for {
			orderRef, err := p.parseExpr(0)
			if err != nil {
				return NodeRef{}, err
			}
			isDesc := false
			if p.curr.Kind == lexer.KindDesc {
				isDesc = true
				p.advance()
			} else if p.curr.Kind == lexer.KindAsc {
				// ASC is the default ordering; consume it so ORDER BY ... ASC
				// remains accepted for every term in a multi-key list.
				p.advance()
			}
			stmt.OrderTerms = append(stmt.OrderTerms, OrderTerm{Expr: orderRef, IsDesc: isDesc})
			if len(stmt.OrderTerms) == 1 {
				// Keep the legacy first-term view in sync for existing planners.
				stmt.OrderBy = orderRef
				stmt.IsDesc = isDesc
			}
			if p.curr.Kind != lexer.KindComma {
				break
			}
			p.advance()
		}
	}

	// LIMIT and OFFSET may appear in either order. Each clause stores an
	// index into the shared Number arena so downstream consumers can inspect
	// the literal without reparsing the source.
	seenLimit := false
	seenOffset := false
	for p.curr.Kind == lexer.KindLimit || p.curr.Kind == lexer.KindOffset {
		isOffset := p.curr.Kind == lexer.KindOffset
		if isOffset {
			if seenOffset {
				return NodeRef{}, fmt.Errorf("duplicate OFFSET clause")
			}
			seenOffset = true
		} else {
			if seenLimit {
				return NodeRef{}, fmt.Errorf("duplicate LIMIT clause")
			}
			seenLimit = true
		}

		p.advance()
		if p.curr.Kind == lexer.KindParam {
			ref, err := p.parseExpr(0)
			if err != nil {
				return NodeRef{}, err
			}
			if isOffset {
				stmt.OffsetExpr = ref
			} else {
				stmt.LimitExpr = ref
			}
			continue
		}
		// pgx/database-sql simple-protocol sanitization may encode an integer
		// LIMIT/OFFSET bind as a quoted numeric literal. PostgreSQL accepts
		// that implicit integer coercion; preserve the source span without
		// treating arbitrary strings as numbers elsewhere in the grammar.
		if p.curr.Kind == lexer.KindString && p.curr.End > p.curr.Start+2 {
			start, end := p.curr.Start+1, p.curr.End-1
			valid := true
			for i := start; i < end; i++ {
				if p.src[i] < '0' || p.src[i] > '9' {
					valid = false
					break
				}
			}
			if valid {
				num := Number{ID: int32(len(p.doc.Numbers)), Start: start, End: end}
				p.doc.Numbers = append(p.doc.Numbers, num)
				if isOffset {
					stmt.Offset = num.ID
				} else {
					stmt.Limit = num.ID
				}
				p.advance()
				continue
			}
		}
		if p.curr.Kind != lexer.KindNumber {
			if isOffset {
				return NodeRef{}, fmt.Errorf("expected number after OFFSET, got %v", p.curr.Kind)
			}
			return NodeRef{}, fmt.Errorf("expected number after LIMIT, got %v", p.curr.Kind)
		}

		num := Number{
			ID:    int32(len(p.doc.Numbers)),
			Start: p.curr.Start,
			End:   p.curr.End,
		}
		p.doc.Numbers = append(p.doc.Numbers, num)
		if isOffset {
			stmt.Offset = num.ID
		} else {
			stmt.Limit = num.ID
		}
		p.advance()
	}

	if p.curr.Kind == lexer.KindUnion || p.curr.Kind == lexer.KindIntersect || p.curr.Kind == lexer.KindExcept {
		stmt.UnionStart = p.curr.Start
		switch p.curr.Kind {
		case lexer.KindUnion:
			stmt.SetOp = SetOpUnion
		case lexer.KindIntersect:
			stmt.SetOp = SetOpIntersect
		case lexer.KindExcept:
			stmt.SetOp = SetOpExcept
		}
		p.advance()
		if p.curr.Kind == lexer.KindAll {
			stmt.SetOpAll = true
			stmt.UnionAll = stmt.SetOp == SetOpUnion
			p.advance()
		}
		if p.curr.Kind != lexer.KindSelect {
			return NodeRef{}, fmt.Errorf("expected SELECT after set operation")
		}
		branch, err := p.parseSelectStmt()
		if err != nil {
			return NodeRef{}, fmt.Errorf("set operation: %w", err)
		}
		stmt.UnionNext = branch
		// The recursive branch is appended before the outer statement, so the
		// outer SELECT receives its final stable arena index here.
		stmt.ID = int32(len(p.doc.SelectStmts))
	}
	stmt.SourceEnd = p.curr.Start
	if stmt.SourceEnd == 0 || stmt.SourceEnd > uint32(len(p.src)) {
		stmt.SourceEnd = uint32(len(p.src))
	}

	// Nested SELECTs are parsed before their containing statement is appended.
	// Allocate the final arena id at append time so a subquery cannot alias the
	// outer statement's id (both previously started at len(SelectStmts)).
	if stmt.ID < int32(len(p.doc.SelectStmts)) {
		stmt.ID = int32(len(p.doc.SelectStmts))
	}
	for i := range localProjections {
		projection := &localProjections[i]
		if projection.Expr.Kind == NodeKindFunctionExpr && projection.Expr.ID >= 0 && int(projection.Expr.ID) < len(p.doc.FunctionExprs) {
			fn := &p.doc.FunctionExprs[projection.Expr.ID]
			if fn.HasWindow && fn.WindowID < 0 {
				fn.WindowID = p.resolveNamedWindow(stmt, fn.WindowNameStart, fn.WindowNameEnd)
				if fn.WindowID < 0 {
					return NodeRef{}, fmt.Errorf("unknown named window %q", string(p.src[fn.WindowNameStart:fn.WindowNameEnd]))
				}
			}
		}
		if projection.Expr.Kind == NodeKindAggregateExpr && projection.Expr.ID >= 0 && int(projection.Expr.ID) < len(p.doc.AggregateExprs) {
			ae := &p.doc.AggregateExprs[projection.Expr.ID]
			if ae.HasWindow && ae.WindowID < 0 {
				ae.WindowID = p.resolveNamedWindow(stmt, ae.WindowNameStart, ae.WindowNameEnd)
				if ae.WindowID < 0 {
					return NodeRef{}, fmt.Errorf("unknown named window %q", string(p.src[ae.WindowNameStart:ae.WindowNameEnd]))
				}
			}
		}
	}
	stmt.ProjectionsStart = int32(len(p.doc.Projections))
	for i := range localProjections {
		localProjections[i].ID = int32(len(p.doc.Projections))
		p.doc.Projections = append(p.doc.Projections, localProjections[i])
	}
	// Publish any backing-array growth into the reusable arena. Without this,
	// append would grow only the statement-local slice header and the next
	// parse would allocate again for every JOIN/GROUP BY/ORDER BY clause.
	p.doc.joinArenas[arenaID] = stmt.Joins
	p.doc.groupByArenas[arenaID] = stmt.GroupBy
	p.doc.orderTermsArenas[arenaID] = stmt.OrderTerms
	p.doc.SelectStmts = append(p.doc.SelectStmts, stmt)
	return NodeRef{Kind: NodeKindSelectStmt, ID: stmt.ID}, nil
}

func (p *Parser) resolveNamedWindow(stmt SelectStmt, start, end uint32) int32 {
	if start >= end {
		return -1
	}
	for i := stmt.WindowDefsStart; i < stmt.WindowDefsStart+stmt.WindowDefsCount; i++ {
		if i < 0 || int(i) >= len(p.doc.NamedWindows) {
			continue
		}
		def := p.doc.NamedWindows[i]
		if bytes.EqualFold(p.src[def.NameStart:def.NameEnd], p.src[start:end]) {
			return def.SpecID
		}
	}
	return -1
}

func (p *Parser) parseProjection() (Projection, error) {
	// SELECT *
	if p.curr.Kind == lexer.KindAsterisk {
		p.advance()
		return Projection{ID: int32(len(p.doc.Projections)), Star: true}, nil
	}
	expr, err := p.parseExpr(0)
	if err != nil {
		return Projection{}, err
	}
	proj := Projection{
		ID:   int32(len(p.doc.Projections)),
		Expr: expr,
	}
	// Optional column alias: SELECT expr AS alias
	// Keywords are accepted as aliases (e.g., AS similarity).
	if p.curr.Kind == lexer.KindAs {
		p.advance()
		if isIdentifierLike(p.curr.Kind) {
			proj.Alias = p.curr.Start
			proj.AliasEnd = p.curr.End
			p.advance()
		}
	}
	return proj, nil
}

// isIdentifierLike returns true for tokens that can serve as column aliases
// (identifiers and non-reserved keywords).
func isIdentifierLike(k lexer.Kind) bool {
	if k == lexer.KindDetach || k == lexer.KindSkip {
		return false
	}
	return k == lexer.KindIdentifier || k >= lexer.KindSelect
}

// isColumnToken accepts SQL keywords that remain legal as unquoted relation or
// column identifiers in the positions handled by the parser. KEY is reserved
// for FOREIGN KEY grammar, and OPTIONAL is reserved for OPTIONAL MATCH, but
// both remain valid PostgreSQL-compatible identifiers in DDL and expressions.
func isColumnToken(k lexer.Kind) bool {
	return k == lexer.KindIdentifier || k == lexer.KindKey || k == lexer.KindOptional
}

func isStringToken(k lexer.Kind) bool {
	return k == lexer.KindString || k == lexer.KindEscapeString
}

func (p *Parser) parseTableExpr() (NodeRef, error) {
	if p.curr.Kind == lexer.KindGraphTable {
		p.advance()
		return p.parseGraphTable()
	}

	// Temporal range relation:
	//   VERSIONS OF table BETWEEN TIMESTAMP start AND TIMESTAMP end [alias]
	// This is deliberately a table-source form rather than an expression so
	// the existing virtual SELECT evaluator can apply WHERE/ORDER/LIMIT to
	// materialized version tuples without creating a catalog relation.
	if p.curr.Kind == lexer.KindVersions {
		t := TableExpr{ID: int32(len(p.doc.TableExprs)), TemporalRange: true}
		p.advance() // VERSIONS
		if err := p.expect(lexer.KindOf); err != nil {
			return NodeRef{}, fmt.Errorf("expected OF after VERSIONS: %w", err)
		}
		if p.curr.Kind != lexer.KindIdentifier {
			return NodeRef{}, fmt.Errorf("expected table name after VERSIONS OF")
		}
		t.Start, t.End = p.curr.Start, p.curr.End
		p.advance()
		if err := p.expect(lexer.KindBetween); err != nil {
			return NodeRef{}, fmt.Errorf("expected BETWEEN after VERSIONS OF table: %w", err)
		}
		if err := p.expect(lexer.KindTimestamp); err != nil {
			return NodeRef{}, fmt.Errorf("expected TIMESTAMP after BETWEEN: %w", err)
		}
		if !isStringToken(p.curr.Kind) && p.curr.Kind != lexer.KindIdentifier && p.curr.Kind != lexer.KindParam {
			return NodeRef{}, fmt.Errorf("expected start timestamp literal or parameter")
		}
		t.RangeStartStart, t.RangeStartEnd = p.curr.Start, p.curr.End
		p.advance()
		if err := p.expect(lexer.KindAnd); err != nil {
			return NodeRef{}, fmt.Errorf("expected AND between temporal range bounds: %w", err)
		}
		if err := p.expect(lexer.KindTimestamp); err != nil {
			return NodeRef{}, fmt.Errorf("expected TIMESTAMP before end bound: %w", err)
		}
		if !isStringToken(p.curr.Kind) && p.curr.Kind != lexer.KindIdentifier && p.curr.Kind != lexer.KindParam {
			return NodeRef{}, fmt.Errorf("expected end timestamp literal or parameter")
		}
		t.RangeEndStart, t.RangeEndEnd = p.curr.Start, p.curr.End
		p.advance()
		if p.curr.Kind == lexer.KindAs {
			p.advance()
		}
		if p.curr.Kind == lexer.KindIdentifier {
			t.Alias, t.AliasEnd = p.curr.Start, p.curr.End
			p.advance()
		}
		p.doc.TableExprs = append(p.doc.TableExprs, t)
		return NodeRef{Kind: NodeKindTableExpr, ID: t.ID}, nil
	}

	// Set-returning function source, e.g. jsonb_array_elements(...) AS elem.
	// Keep this in the table-expression arena rather than inventing a second
	// parser path so the executor can evaluate arguments with normal scope.
	if p.curr.Kind == lexer.KindIdentifier && p.next.Kind == lexer.KindLeftParen {
		start := p.curr.Start
		fn, err := p.parseFunctionExpr()
		if err != nil {
			return NodeRef{}, fmt.Errorf("table function: %w", err)
		}
		t := TableExpr{ID: int32(len(p.doc.TableExprs)), Start: start, End: p.curr.Start, Function: fn, IsFunction: true}
		if p.curr.Kind == lexer.KindAs {
			p.advance()
		}
		if p.curr.Kind == lexer.KindIdentifier {
			t.Alias, t.AliasEnd = p.curr.Start, p.curr.End
			t.End = p.curr.End
			p.advance()
		} else {
			return NodeRef{}, fmt.Errorf("table function requires an alias")
		}
		p.doc.TableExprs = append(p.doc.TableExprs, t)
		return NodeRef{Kind: NodeKindTableExpr, ID: t.ID}, nil
	}

	// Derived table: FROM (SELECT ...) [AS] alias. The nested SELECT remains
	// in the same AST arena so the executor can evaluate it as an owned
	// virtual relation without creating a catalog table or WAL record.
	if p.curr.Kind == lexer.KindLeftParen && p.next.Kind == lexer.KindSelect {
		start := p.curr.Start
		p.advance() // consume '('
		stmtRef, err := p.parseSelectStmt()
		if err != nil {
			return NodeRef{}, fmt.Errorf("derived table: %w", err)
		}
		closeEnd := p.curr.End
		if err := p.expect(lexer.KindRightParen); err != nil {
			return NodeRef{}, fmt.Errorf("derived table: %w", err)
		}
		t := TableExpr{ID: int32(len(p.doc.TableExprs)), Start: start, End: closeEnd, Derived: stmtRef, IsDerived: true}
		if p.curr.Kind == lexer.KindAs {
			p.advance()
		}
		if p.curr.Kind != lexer.KindIdentifier {
			return NodeRef{}, fmt.Errorf("derived table requires an alias")
		}
		t.Alias = p.curr.Start
		t.AliasEnd = p.curr.End
		t.End = p.curr.End
		p.advance()
		p.doc.TableExprs = append(p.doc.TableExprs, t)
		return NodeRef{Kind: NodeKindTableExpr, ID: t.ID}, nil
	}

	if p.curr.Kind == lexer.KindIdentifier {
		t := TableExpr{
			ID:    int32(len(p.doc.TableExprs)),
			Start: p.curr.Start,
			End:   p.curr.End,
		}
		p.advance()
		// Preserve a schema-qualified relation such as
		// information_schema.columns or pg_catalog.pg_class as one table
		// source span.  The executor may rewrite/intercept system schemas,
		// while pgwire still needs the parser to discover parameters and
		// describe the statement before execution.
		if p.curr.Kind == lexer.KindDot {
			p.advance()
			if !isColumnToken(p.curr.Kind) {
				return NodeRef{}, fmt.Errorf("expected relation name after schema qualifier")
			}
			t.End = p.curr.End
			p.advance()
		}
		parseTemporal := func() error {
			if p.curr.Kind != lexer.KindAs || p.next.Kind != lexer.KindOf {
				return nil
			}
			p.advance() // consume AS
			p.advance() // consume OF
			if p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("lsn")) {
				p.advance() // consume LSN
				if !isStringToken(p.curr.Kind) && p.curr.Kind != lexer.KindNumber && p.curr.Kind != lexer.KindIdentifier && p.curr.Kind != lexer.KindParam {
					return fmt.Errorf("expected LSN literal or parameter after AS OF LSN")
				}
				t.TemporalLSN = true
				t.LSNStart = p.curr.Start
				t.LSNEnd = p.curr.End
				p.advance()
				return nil
			}
			if err := p.expect(lexer.KindTimestamp); err != nil {
				return fmt.Errorf("expected TIMESTAMP after AS OF, got %v", p.curr.Kind)
			}
			// A timestamp may be a literal or a native $N/@name parameter.
			// The optimizer resolves the latter from the bound ParameterSet.
			if !isStringToken(p.curr.Kind) && p.curr.Kind != lexer.KindIdentifier && p.curr.Kind != lexer.KindParam {
				return fmt.Errorf("expected timestamp literal or parameter after AS OF TIMESTAMP")
			}
			t.Temporal = true
			t.TimestampStart = p.curr.Start
			t.TimestampEnd = p.curr.End
			p.advance()
			return nil
		}
		// Accept both PostgreSQL-style placements used by clients:
		//   table alias AS OF TIMESTAMP value
		//   table AS OF TIMESTAMP value alias
		if p.curr.Kind == lexer.KindAs && p.next.Kind == lexer.KindOf {
			if err := parseTemporal(); err != nil {
				return NodeRef{}, err
			}
		}
		if p.curr.Kind == lexer.KindAs {
			p.advance()
			if p.curr.Kind != lexer.KindIdentifier {
				return NodeRef{}, errors.New("expected table alias after AS")
			}
			t.Alias = p.curr.Start
			t.AliasEnd = p.curr.End
			p.advance()
		}
		if p.curr.Kind == lexer.KindIdentifier && !bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("window")) {
			t.Alias = p.curr.Start
			t.AliasEnd = p.curr.End
			p.advance()
		}
		if !t.Temporal && !t.TemporalLSN {
			if err := parseTemporal(); err != nil {
				return NodeRef{}, err
			}
		}
		p.doc.TableExprs = append(p.doc.TableExprs, t)
		return NodeRef{Kind: NodeKindTableExpr, ID: t.ID}, nil
	}

	return NodeRef{}, errors.New("expected table expression")
}

func (p *Parser) parseGraphTable() (NodeRef, error) {
	gt := GraphTable{
		ID: int32(len(p.doc.GraphTables)),
	}
	// In standard SQL/PGQ, it looks like GRAPH_TABLE(my_graph MATCH (a)-[e]->(b))
	if err := p.expect(lexer.KindLeftParen); err != nil {
		return NodeRef{}, err
	}
	if p.curr.Kind != lexer.KindIdentifier {
		return NodeRef{}, errors.New("expected graph name")
	}
	gt.TableStart = p.curr.Start
	gt.TableEnd = p.curr.End
	p.advance()

	if err := p.expect(lexer.KindMatch); err != nil {
		return NodeRef{}, err
	}

	matchRef, err := p.parseMatchPath()
	if err != nil {
		return NodeRef{}, err
	}
	gt.MatchPath = matchRef

	if err := p.expect(lexer.KindRightParen); err != nil {
		return NodeRef{}, err
	}

	p.doc.GraphTables = append(p.doc.GraphTables, gt)
	return NodeRef{Kind: NodeKindGraphTable, ID: gt.ID}, nil
}

// parseComputeLeidenStmt parses COMPUTE LEIDEN FROM MATCH ... [OPTIONS (...)]
// and appends a root node to doc.Nodes for standalone statement execution.
func (p *Parser) parseComputeLeidenStmt() (NodeRef, error) {
	stmt, err := p.parseComputeLeidenStmtBody()
	if err != nil {
		return NodeRef{}, err
	}
	p.doc.ComputeLeidenStmts = append(p.doc.ComputeLeidenStmts, stmt)
	return NodeRef{Kind: NodeKindComputeLeidenStmt, ID: stmt.ID}, nil
}

// parseComputeLeidenStmtBody parses the COMPUTE LEIDEN statement body
// without appending a root node. Used by both the standalone statement
// dispatcher and the CTE AS clause parser.
func (p *Parser) parseComputeLeidenStmtBody() (ComputeLeidenStmt, error) {
	p.advance() // consume COMPUTE

	if p.curr.Kind != lexer.KindLeiden {
		return ComputeLeidenStmt{}, fmt.Errorf("expected LEIDEN after COMPUTE, got %v", p.curr.Kind)
	}
	p.advance() // consume LEIDEN

	if p.curr.Kind != lexer.KindFrom {
		return ComputeLeidenStmt{}, fmt.Errorf("expected FROM after LEIDEN, got %v", p.curr.Kind)
	}
	p.advance() // consume FROM

	if p.curr.Kind != lexer.KindMatch {
		return ComputeLeidenStmt{}, fmt.Errorf("expected MATCH after FROM, got %v", p.curr.Kind)
	}

	// Parse the MATCH path using the existing parser.
	matchRef := p.parseMatchPathNode()
	if matchRef.Kind != NodeKindMatchPath {
		return ComputeLeidenStmt{}, fmt.Errorf("expected a valid MATCH path")
	}

	stmt := ComputeLeidenStmt{
		ID:        int32(len(p.doc.ComputeLeidenStmts)),
		MatchPath: matchRef,
	}

	// Parse optional OPTIONS (...).
	if p.curr.Kind == lexer.KindOptions {
		if err := p.parseLeidenOptions(&stmt); err != nil {
			return ComputeLeidenStmt{}, err
		}
	}

	return stmt, nil
}

// parseLeidenOptions parses OPTIONS (name = value, ...).
func (p *Parser) parseLeidenOptions(stmt *ComputeLeidenStmt) error {
	p.advance() // consume OPTIONS

	if err := p.expect(lexer.KindLeftParen); err != nil {
		return fmt.Errorf("expected '(' after OPTIONS: %w", err)
	}

	stmt.OptionsStart = int32(len(p.doc.LeidenOptions))
	seenNames := make(map[string]bool)

	for {
		if p.curr.Kind == lexer.KindRightParen {
			p.advance()
			break
		}

		if p.curr.Kind != lexer.KindIdentifier {
			return fmt.Errorf("expected option name (identifier), got %v", p.curr.Kind)
		}

		name := string(p.src[p.curr.Start:p.curr.End])
		nameLower := toLower(name)
		nameStart := p.curr.Start
		nameEnd := p.curr.End
		p.advance() // consume name

		if p.curr.Kind != lexer.KindEquals {
			return fmt.Errorf("expected '=' after option %q, got %v", name, p.curr.Kind)
		}
		p.advance() // consume '='

		// Value: number, identifier, or string literal.
		if p.curr.Kind != lexer.KindNumber &&
			p.curr.Kind != lexer.KindIdentifier &&
			!isStringToken(p.curr.Kind) {
			return fmt.Errorf("expected option value for %q, got %v", name, p.curr.Kind)
		}

		valueKind := NodeKindUnknown
		var valueID int32
		switch p.curr.Kind {
		case lexer.KindNumber:
			valueKind = NodeKindNumber
			n := Number{ID: int32(len(p.doc.Numbers)), Start: p.curr.Start, End: p.curr.End}
			p.doc.Numbers = append(p.doc.Numbers, n)
			valueID = n.ID
		case lexer.KindIdentifier:
			valueKind = NodeKindIdentifier
			id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
			p.doc.Identifiers = append(p.doc.Identifiers, id)
			valueID = id.ID
		case lexer.KindString, lexer.KindEscapeString:
			valueKind = NodeKindString
			s := StringLiteral{ID: int32(len(p.doc.Strings)), Start: p.curr.Start, End: p.curr.End, Escape: p.curr.Kind == lexer.KindEscapeString}
			p.doc.Strings = append(p.doc.Strings, s)
			valueID = s.ID
		}
		p.advance() // consume value

		optKind, ok := resolveLeidenOptionKind(nameLower)
		if !ok {
			return fmt.Errorf("unknown option %q", name)
		}

		if seenNames[nameLower] {
			return fmt.Errorf("duplicate option %q", name)
		}
		seenNames[nameLower] = true

		p.doc.LeidenOptions = append(p.doc.LeidenOptions, LeidenOption{
			Kind:      optKind,
			NameStart: nameStart,
			NameEnd:   nameEnd,
			Value:     NodeRef{Kind: valueKind, ID: valueID},
		})
		stmt.OptionsCount++

		// Comma-separated entries: after a value, only ',' or ')' are valid.
		if p.curr.Kind == lexer.KindComma {
			p.advance()
			// Trailing comma before ')' is an error.
			if p.curr.Kind == lexer.KindRightParen {
				return fmt.Errorf("trailing comma in OPTIONS")
			}
		} else if p.curr.Kind != lexer.KindRightParen {
			return fmt.Errorf("expected ',' or ')' after option value, got %v", p.curr.Kind)
		}
	}

	if stmt.OptionsCount == 0 {
		return fmt.Errorf("OPTIONS must not be empty")
	}

	return nil
}

// resolveLeidenOptionKind returns the LeidenOptionKind for a case-insensitive
// option name. The name must already be lowercased.
func resolveLeidenOptionKind(name string) (LeidenOptionKind, bool) {
	switch name {
	case "resolution":
		return LeidenOptionResolution, true
	case "iterations":
		return LeidenOptionIterations, true
	case "max_levels":
		return LeidenOptionMaxLevels, true
	case "max_local_moving_passes":
		return LeidenOptionMaxLocalMovingPasses, true
	case "min_hops":
		return LeidenOptionMinHops, true
	case "max_hops":
		return LeidenOptionMaxHops, true
	case "max_vertices":
		return LeidenOptionMaxVertices, true
	case "max_edges":
		return LeidenOptionMaxEdges, true
	case "edge_kind":
		return LeidenOptionEdgeKind, true
	case "direction":
		return LeidenOptionDirection, true
	default:
		return 0, false
	}
}

// toLower returns an ASCII-lowercased copy of s. The lexer operates on ASCII
// identifiers so a full unicode case fold is unnecessary.
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}

// parseMatchPathNode is called from parseExpr when KindMatch appears in WHERE.
func (p *Parser) parseMatchPathNode() NodeRef {
	p.advance() // consume MATCH
	ref, err := p.parseMatchPath()
	if err != nil {
		return NodeRef{}
	}
	return ref
}

func (p *Parser) parseMatchPath() (NodeRef, error) {
	mp := MatchPath{
		ID:             int32(len(p.doc.MatchPaths)),
		PathNodesStart: int32(len(p.doc.Nodes)),
	}
	// Cypher permits a path variable before the pattern, for example
	// `p = (a)-[:KNOWS]->(b)`. Preserve the alias on the path so SQL/PGQ
	// adapters can expose a stable path-valued projection without reparsing.
	if p.curr.Kind == lexer.KindIdentifier && p.next.Kind == lexer.KindEquals {
		mp.PathAlias = p.curr.Start
		mp.PathAliasEnd = p.curr.End
		p.advance()
		p.advance()
	}

	for {
		if p.curr.Kind == lexer.KindLeftParen {
			// Vertex
			p.advance()
			v := Vertex{
				ID: int32(len(p.doc.Vertexes)),
			}
			if isColumnToken(p.curr.Kind) {
				v.Alias = p.curr.Start
				v.AliasEnd = p.curr.End
				p.advance()
			}
			if p.curr.Kind == lexer.KindColon {
				v.LabelsStart = int32(len(p.doc.VertexLabels))
				for p.curr.Kind == lexer.KindColon {
					p.advance()
					if p.curr.Kind != lexer.KindIdentifier {
						return NodeRef{}, fmt.Errorf("expected vertex label after colon")
					}
					label := VertexLabel{Start: p.curr.Start, End: p.curr.End}
					p.doc.VertexLabels = append(p.doc.VertexLabels, label)
					v.LabelsCount++
					if v.LabelsCount == 1 {
						v.LabelStart, v.LabelEnd = label.Start, label.End
					}
					p.advance()
				}
			}
			if p.curr.Kind == lexer.KindLeftBrace {
				predicate, err := p.parseEdgePropertyBlock()
				if err != nil {
					return NodeRef{}, fmt.Errorf("vertex properties: %w", err)
				}
				p.qualifyInlinePropertyPredicate(predicate, v.Alias, v.AliasEnd)
				v.Predicate = predicate
			}
			if err := p.expect(lexer.KindRightParen); err != nil {
				return NodeRef{}, err
			}
			p.doc.Vertexes = append(p.doc.Vertexes, v)
			p.doc.Nodes = append(p.doc.Nodes, NodeRef{Kind: NodeKindVertex, ID: v.ID})
			mp.PathNodesCount++
		} else {
			break
		}

		// Optional Edge
		if p.curr.Kind == lexer.KindDash || p.curr.Kind == lexer.KindArrowLeft {
			e := Edge{
				ID: int32(len(p.doc.Edges)),
			}
			if p.curr.Kind == lexer.KindArrowLeft {
				e.Direction = -1
				p.advance()
			} else {
				p.advance() // consume dash
			}

			// Cypher also permits an anonymous property map directly on a
			// directed relationship: -{weight > 0.5}->(b). Lower it to the
			// same edge predicate AST used by bracketed relationships.
			if p.curr.Kind == lexer.KindLeftBrace {
				predicate, err := p.parseEdgePropertyBlock()
				if err != nil {
					return NodeRef{}, fmt.Errorf("anonymous edge properties: %w", err)
				}
				e.Predicate = predicate
			}
			if p.curr.Kind == lexer.KindLeftBracket {
				p.advance()
				if p.curr.Kind == lexer.KindIdentifier {
					e.Alias = p.curr.Start
					e.AliasEnd = p.curr.End
					p.advance()
				}
				if p.curr.Kind == lexer.KindColon {
					p.advance()
					if p.curr.Kind == lexer.KindIdentifier {
						e.TypeStart = p.curr.Start
						e.TypeEnd = p.curr.End
						p.advance()
					} else {
						return NodeRef{}, fmt.Errorf("expected edge type after colon")
					}
				}
				// Property-map sugar: [r:RELATES {weight > 0.5, type: 'strong'}].
				// The properties are kept in the existing expression arena so the
				// optimizer can lower them into the same edge traversal filters used
				// by the explicit WHERE form.
				if p.curr.Kind == lexer.KindLeftBrace && p.next.Kind != lexer.KindNumber {
					predicate, err := p.parseEdgePropertyBlock()
					if err != nil {
						return NodeRef{}, fmt.Errorf("edge properties: %w", err)
					}
					e.Predicate = predicate
				}
				// Quantifier inside bracket: [e*1..3] or [e:TYPE*1..3]
				if p.curr.Kind == lexer.KindAsterisk || p.curr.Kind == lexer.KindPlus || p.curr.Kind == lexer.KindLeftBrace {
					switch p.curr.Kind {
					case lexer.KindLeftBrace:
						p.advance()
						e.QuantMin = p.parseUint16()
						p.expect(lexer.KindComma)
						e.QuantMax = p.parseUint16()
						p.expect(lexer.KindRightBrace)
					case lexer.KindPlus:
						e.QuantMin = 1
						e.QuantMax = QuantUnbounded
						p.advance()
					case lexer.KindAsterisk:
						p.advance()
						if p.curr.Kind == lexer.KindNumber {
							e.QuantMin = p.parseUint16()
							p.expect(lexer.KindDot)
							p.expect(lexer.KindDot)
							e.QuantMax = p.parseUint16()
						} else {
							e.QuantMin = 0
							e.QuantMax = QuantUnbounded
						}
					}
				}
				if p.curr.Kind == lexer.KindWhere {
					p.advance() // consume edge-local WHERE
					predicate, err := p.parseExpr(0)
					if err != nil {
						return NodeRef{}, fmt.Errorf("edge predicate: %w", err)
					}
					if e.Predicate.Kind == NodeKindUnknown {
						e.Predicate = predicate
					} else {
						and := BinaryExpr{
							ID:       int32(len(p.doc.BinaryExprs)),
							Left:     e.Predicate,
							Right:    predicate,
							Operator: uint8(lexer.KindAnd),
						}
						p.doc.BinaryExprs = append(p.doc.BinaryExprs, and)
						e.Predicate = NodeRef{Kind: NodeKindBinaryExpr, ID: and.ID}
					}
				}
				if err := p.expect(lexer.KindRightBracket); err != nil {
					return NodeRef{}, err
				}
			}

			if p.curr.Kind == lexer.KindArrowRight {
				e.Direction = 1
				p.advance()
			} else if p.curr.Kind == lexer.KindDash {
				p.advance()
			} else {
				return NodeRef{}, errors.New("expected edge closure")
			}

			// Quantifier
			switch p.curr.Kind {
			case lexer.KindLeftBrace:
				p.advance() // consume {
				e.QuantMin = p.parseUint16()
				p.expect(lexer.KindComma)
				e.QuantMax = p.parseUint16()
				p.expect(lexer.KindRightBrace)
			case lexer.KindPlus:
				e.QuantMin = 1
				e.QuantMax = QuantUnbounded
				p.advance()
			case lexer.KindAsterisk:
				p.advance()
				if p.curr.Kind == lexer.KindNumber {
					e.QuantMin = p.parseUint16()
					if err := p.expect(lexer.KindDot); err != nil {
						return NodeRef{}, err
					}
					if err := p.expect(lexer.KindDot); err != nil {
						return NodeRef{}, err
					}
					e.QuantMax = p.parseUint16()
				} else {
					e.QuantMin = 0
					e.QuantMax = QuantUnbounded
				}
			}
			p.doc.Edges = append(p.doc.Edges, e)
			p.doc.Nodes = append(p.doc.Nodes, NodeRef{Kind: NodeKindEdge, ID: e.ID})
			mp.PathNodesCount++
		} else {
			break
		}
	}

	p.doc.MatchPaths = append(p.doc.MatchPaths, mp)
	return NodeRef{Kind: NodeKindMatchPath, ID: mp.ID}, nil
}

// parseEdgePropertyBlock parses the compact property predicate form used by
// graph patterns, for example {weight > 0.5, type: 'strong'}. A colon is
// normalized to equality so both forms share the existing BinaryExpr arena.
// Commas are normalized to AND, while explicit AND/OR preserve their boolean
// shape for the optimizer.
func (p *Parser) parseEdgePropertyBlock() (NodeRef, error) {
	if err := p.expect(lexer.KindLeftBrace); err != nil {
		return NodeRef{}, err
	}

	if p.curr.Kind == lexer.KindRightBrace {
		return NodeRef{}, errors.New("empty edge property block")
	}
	combined, err := p.parseEdgePropertyExpr(0)
	if err != nil {
		return NodeRef{}, err
	}
	if err := p.expect(lexer.KindRightBrace); err != nil {
		return NodeRef{}, err
	}
	return combined, nil
}

// qualifyInlinePropertyPredicate attaches the vertex alias to the left side
// of each property comparison. Edge property blocks are intentionally kept
// unqualified because their evaluator resolves edge fields separately.
func (p *Parser) qualifyInlinePropertyPredicate(ref NodeRef, aliasStart, aliasEnd uint32) {
	if ref.Kind != NodeKindBinaryExpr || ref.ID < 0 || int(ref.ID) >= len(p.doc.BinaryExprs) {
		return
	}
	be := &p.doc.BinaryExprs[ref.ID]
	if be.Operator == uint8(lexer.KindAnd) || be.Operator == uint8(lexer.KindOr) {
		p.qualifyInlinePropertyPredicate(be.Left, aliasStart, aliasEnd)
		p.qualifyInlinePropertyPredicate(be.Right, aliasStart, aliasEnd)
		return
	}
	if be.Left.Kind != NodeKindIdentifier || be.Left.ID < 0 || int(be.Left.ID) >= len(p.doc.Identifiers) {
		return
	}
	id := &p.doc.Identifiers[be.Left.ID]
	if id.QualStart == id.QualEnd && aliasEnd > aliasStart {
		id.QualStart = aliasStart
		id.QualEnd = aliasEnd
	}
}

func (p *Parser) parseEdgePropertyExpr(precedence int) (NodeRef, error) {
	left, err := p.parseEdgePropertyComparison()
	if err != nil {
		return NodeRef{}, err
	}
	for {
		op := p.curr.Kind
		if op == lexer.KindComma {
			op = lexer.KindAnd
		} else if op != lexer.KindAnd && op != lexer.KindOr {
			break
		}
		prec := operatorPrecedence(op)
		if prec <= precedence {
			break
		}
		p.advance()
		if p.curr.Kind == lexer.KindRightBrace || p.curr.Kind == lexer.KindEOF {
			return NodeRef{}, errors.New("trailing boolean operator in edge property block")
		}
		right, err := p.parseEdgePropertyExpr(prec)
		if err != nil {
			return NodeRef{}, err
		}
		be := BinaryExpr{
			ID:       int32(len(p.doc.BinaryExprs)),
			Left:     left,
			Right:    right,
			Operator: uint8(op),
		}
		p.doc.BinaryExprs = append(p.doc.BinaryExprs, be)
		left = NodeRef{Kind: NodeKindBinaryExpr, ID: be.ID}
	}
	return left, nil
}

func (p *Parser) parseEdgePropertyComparison() (NodeRef, error) {
	if !isColumnToken(p.curr.Kind) {
		return NodeRef{}, fmt.Errorf("expected edge property name, got %v", p.curr.Kind)
	}
	id := Identifier{
		ID:    int32(len(p.doc.Identifiers)),
		Start: p.curr.Start,
		End:   p.curr.End,
	}
	p.doc.Identifiers = append(p.doc.Identifiers, id)
	left := NodeRef{Kind: NodeKindIdentifier, ID: id.ID}
	p.advance()

	op := lexer.KindEquals
	if p.curr.Kind == lexer.KindColon {
		p.advance()
	} else {
		switch p.curr.Kind {
		case lexer.KindEquals, lexer.KindNotEqual,
			lexer.KindGreaterThan, lexer.KindGreaterEqual,
			lexer.KindLessThan, lexer.KindLessEqual:
			op = p.curr.Kind
			p.advance()
		default:
			return NodeRef{}, fmt.Errorf("expected edge property comparison, got %v", p.curr.Kind)
		}
	}

	// Comparison RHS values are scalar edge-property values. Use comparison
	// precedence so AND/OR remain at the property-expression level.
	right, err := p.parseExpr(operatorPrecedence(op))
	if err != nil {
		return NodeRef{}, err
	}
	leaf := BinaryExpr{
		ID:       int32(len(p.doc.BinaryExprs)),
		Left:     left,
		Right:    right,
		Operator: uint8(op),
	}
	p.doc.BinaryExprs = append(p.doc.BinaryExprs, leaf)
	return NodeRef{Kind: NodeKindBinaryExpr, ID: leaf.ID}, nil
}

// Simple Pratt parser stub for expressions.
func (p *Parser) parseExpr(precedence int) (NodeRef, error) {
	// NUD (Null Denotation)
	var left NodeRef
	switch p.curr.Kind {
	case lexer.KindIdentifier, lexer.KindKey, lexer.KindExcluded:
		// PostgreSQL ARRAY[...] constructors are represented as one
		// source-backed identifier span. The SQL executor interprets the span
		// without allocating an AST array node, preserving the existing arena
		// contract while allowing JSON ?|/?& operands.
		if p.curr.Kind == lexer.KindIdentifier && p.next.Kind == lexer.KindLeftBracket &&
			bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("array")) {
			start := p.curr.Start
			p.advance() // ARRAY
			p.advance() // [
			for p.curr.Kind != lexer.KindRightBracket && p.curr.Kind != lexer.KindEOF {
				p.advance()
			}
			end := p.curr.End
			if err := p.expect(lexer.KindRightBracket); err != nil {
				return NodeRef{}, fmt.Errorf("ARRAY constructor: %w", err)
			}
			id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: start, End: end}
			p.doc.Identifiers = append(p.doc.Identifiers, id)
			left = NodeRef{Kind: NodeKindIdentifier, ID: id.ID}
			break
		}
		if p.next.Kind == lexer.KindLeftParen {
			if bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("cast")) {
				var err error
				left, err = p.parseCastFunctionExpr()
				if err != nil {
					return NodeRef{}, err
				}
				break
			}
			if isOrderedSetAggregateName(p.src[p.curr.Start:p.curr.End]) {
				return p.parseOrderedSetAggregate()
			}
			if bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("vector_avg")) {
				return p.parseAggregate(), nil
			}
			var err error
			left, err = p.parseFunctionExpr()
			if err != nil {
				return NodeRef{}, err
			}
			break
		}
		resolvedKind := ResolvedKindUnknown
		if p.curr.Kind == lexer.KindExcluded {
			resolvedKind = ResolvedKindExcluded
		} else if p.curr.Kind == lexer.KindIdentifier {
			literal := p.src[p.curr.Start:p.curr.End]
			if bytes.EqualFold(literal, []byte("true")) || bytes.EqualFold(literal, []byte("false")) {
				resolvedKind = ResolvedKindLiteral
			}
		}
		id := Identifier{
			ID:           int32(len(p.doc.Identifiers)),
			Start:        p.curr.Start,
			End:          p.curr.End,
			ResolvedKind: resolvedKind,
		}
		left = NodeRef{Kind: NodeKindIdentifier, ID: id.ID}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		p.advance()

		// Peek for dot property access e.g., a.vec — capture the qualifier so
		// the binder can resolve s.owner_id against the FROM alias s.
		if p.curr.Kind == lexer.KindDot {
			p.advance() // dot
			if isColumnToken(p.curr.Kind) {
				id.QualStart = id.Start
				id.QualEnd = id.End
				id.Start = p.curr.Start
				id.End = p.curr.End
				// The identifier was appended before we saw the dot. Publish the
				// qualified form back into the SoA document so binder/optimizer
				// observe title with qualifier doc, rather than the stale doc token.
				p.doc.Identifiers[id.ID] = id
				p.advance()
			}
		}

	case lexer.KindCount, lexer.KindSum, lexer.KindAvg, lexer.KindMin, lexer.KindMax:
		left = p.parseAggregate()

	case lexer.KindMatch:
		// WHERE MATCH (c)-[:PURCHASED]->(p:Product)
		// Lower to a MatchPath like JOIN MATCH uses.
		left = p.parseMatchPathNode()

	case lexer.KindParam:
		// $prompt_vec — named query parameter
		id := Identifier{
			ID:    int32(len(p.doc.Identifiers)),
			Start: p.curr.Start,
			End:   p.curr.End,
		}
		left = NodeRef{Kind: NodeKindIdentifier, ID: id.ID}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		p.advance()

	case lexer.KindGraphCentrality:
		if p.next.Kind != lexer.KindLeftParen {
			id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
			left = NodeRef{Kind: NodeKindIdentifier, ID: id.ID}
			p.doc.Identifiers = append(p.doc.Identifiers, id)
			p.advance()
			break
		}
		p.advance() // consume GRAPH_CENTRALITY
		p.expect(lexer.KindLeftParen)
		operand, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		p.expect(lexer.KindRightParen)
		gm := GraphMetricExpr{ID: int32(len(p.doc.GraphMetrics)), Operand: operand, Kind: 0}
		left = NodeRef{Kind: NodeKindGraphMetric, ID: gm.ID}
		p.doc.GraphMetrics = append(p.doc.GraphMetrics, gm)

	case lexer.KindSimilarity, lexer.KindVectorDistance, lexer.KindArrayCosineSimilarity:
		// Function names are legal SELECT aliases (for example
		// ORDER BY similarity). Treat the keyword as an identifier unless it
		// is actually followed by a call parenthesis.
		if p.next.Kind != lexer.KindLeftParen {
			id := Identifier{
				ID:    int32(len(p.doc.Identifiers)),
				Start: p.curr.Start,
				End:   p.curr.End,
			}
			left = NodeRef{Kind: NodeKindIdentifier, ID: id.ID}
			p.doc.Identifiers = append(p.doc.Identifiers, id)
			p.advance()
			break
		}
		isSim := p.curr.Kind == lexer.KindSimilarity || p.curr.Kind == lexer.KindArrayCosineSimilarity
		p.advance()
		if err := p.expect(lexer.KindLeftParen); err != nil {
			return NodeRef{}, err
		}

		vecA, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		if err := p.expect(lexer.KindComma); err != nil {
			return NodeRef{}, err
		}
		vecB, err := p.parseExpr(0) // Usually an array, but we just parse it as an expr.
		if err != nil {
			return NodeRef{}, err
		}
		if err := p.expect(lexer.KindRightParen); err != nil {
			return NodeRef{}, err
		}

		vf := VectorFunc{
			ID:       int32(len(p.doc.VectorFuncs)),
			IsMaxSim: isSim,
			VectorA:  vecA,
			VectorB:  vecB,
		}
		p.doc.VectorFuncs = append(p.doc.VectorFuncs, vf)
		left = NodeRef{Kind: NodeKindVectorFunc, ID: vf.ID}

	case lexer.KindString, lexer.KindEscapeString:
		sl := StringLiteral{
			ID:     int32(len(p.doc.Strings)),
			Start:  p.curr.Start,
			End:    p.curr.End,
			Escape: p.curr.Kind == lexer.KindEscapeString,
		}
		left = NodeRef{Kind: NodeKindString, ID: sl.ID}
		p.doc.Strings = append(p.doc.Strings, sl)
		p.advance()
	case lexer.KindNull:
		// Keep SQL NULL as a literal identifier node.  This is the same
		// representation used by DEFAULT and lets lowering preserve NULL
		// separately from the quoted string "NULL".
		id := Identifier{
			ID:           int32(len(p.doc.Identifiers)),
			Start:        p.curr.Start,
			End:          p.curr.End,
			ResolvedKind: ResolvedKindLiteral,
		}
		left = NodeRef{Kind: NodeKindIdentifier, ID: id.ID}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		p.advance()
	case lexer.KindNow, lexer.KindNullif:
		var err error
		left, err = p.parseFunctionExpr()
		if err != nil {
			return NodeRef{}, err
		}
	case lexer.KindCase:
		return p.parseCaseExpr()
	case lexer.KindNumber:
		num := Number{
			ID:    int32(len(p.doc.Numbers)),
			Start: p.curr.Start,
			End:   p.curr.End,
		}
		left = NodeRef{Kind: NodeKindNumber, ID: num.ID}
		p.doc.Numbers = append(p.doc.Numbers, num)
		p.advance()
	case lexer.KindPlus, lexer.KindDash:
		// Signed numeric literals remain one Number span in the AST. Keeping
		// the sign in the source span preserves exact decimal/scientific text
		// without allocating or introducing a unary node for a scalar literal.
		signStart := p.curr.Start
		p.advance()
		if p.curr.Kind != lexer.KindNumber {
			return NodeRef{}, fmt.Errorf("expected numeric literal after sign")
		}
		num := Number{ID: int32(len(p.doc.Numbers)), Start: signStart, End: p.curr.End}
		p.doc.Numbers = append(p.doc.Numbers, num)
		left = NodeRef{Kind: NodeKindNumber, ID: num.ID}
		p.advance()
	case lexer.KindLeftBracket:
		// Graph pattern comprehension: [(a)-[:KNOWS]->(b) | b.id].
		// Numeric/vector arrays retain the legacy source-backed identifier path.
		if p.next.Kind == lexer.KindLeftParen {
			p.advance() // consume [
			matchRef, err := p.parseMatchPath()
			if err != nil {
				return NodeRef{}, fmt.Errorf("pattern comprehension: %w", err)
			}
			predicate := NodeRef{}
			if p.curr.Kind == lexer.KindWhere {
				p.advance()
				predicate, err = p.parseExpr(0)
				if err != nil {
					return NodeRef{}, fmt.Errorf("pattern comprehension WHERE: %w", err)
				}
			}
			if err := p.expect(lexer.KindPipe); err != nil {
				return NodeRef{}, fmt.Errorf("pattern comprehension: %w", err)
			}
			projection, err := p.parseExpr(0)
			if err != nil {
				return NodeRef{}, fmt.Errorf("pattern comprehension projection: %w", err)
			}
			if err := p.expect(lexer.KindRightBracket); err != nil {
				return NodeRef{}, fmt.Errorf("pattern comprehension: %w", err)
			}
			pc := PatternComprehension{ID: int32(len(p.doc.PatternComprehensions)), MatchPath: matchRef, Predicate: predicate, Projection: projection}
			p.doc.PatternComprehensions = append(p.doc.PatternComprehensions, pc)
			left = NodeRef{Kind: NodeKindPatternComprehension, ID: pc.ID}
			break
		}
		// simple array stub just for testing [1.0, 0.5]
		// we treat the whole array as an identifier node for now in the AST to save defining Array literal AST nodes
		start := p.curr.Start
		for p.curr.Kind != lexer.KindEOF && p.curr.Kind != lexer.KindRightBracket {
			p.advance()
		}
		end := p.curr.End
		if err := p.expect(lexer.KindRightBracket); err != nil {
			return NodeRef{}, err
		}
		id := Identifier{
			ID:    int32(len(p.doc.Identifiers)),
			Start: start,
			End:   end,
		}
		left = NodeRef{Kind: NodeKindIdentifier, ID: id.ID}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
	case lexer.KindShortestPath:
		p.advance()
		if err := p.expect(lexer.KindLeftParen); err != nil {
			return NodeRef{}, fmt.Errorf("shortestPath: %w", err)
		}
		matchRef, err := p.parseMatchPath()
		if err != nil {
			return NodeRef{}, err
		}
		if err := p.expect(lexer.KindRightParen); err != nil {
			return NodeRef{}, fmt.Errorf("shortestPath: %w", err)
		}
		p.doc.MatchPaths[matchRef.ID].Shortest = true
		sp := ShortestPathExpr{ID: int32(len(p.doc.ShortestPaths)), MatchPath: matchRef}
		p.doc.ShortestPaths = append(p.doc.ShortestPaths, sp)
		left = NodeRef{Kind: NodeKindShortestPath, ID: sp.ID}

	case lexer.KindExists:
		// EXISTS (SELECT ...) is a subquery expression with an explicit
		// marker; keeping it in the expression arena avoids SQL rewriting.
		p.advance() // consume EXISTS
		if err := p.expect(lexer.KindLeftParen); err != nil {
			return NodeRef{}, fmt.Errorf("EXISTS: %w", err)
		}
		if p.curr.Kind != lexer.KindSelect {
			return NodeRef{}, fmt.Errorf("EXISTS expects a SELECT subquery")
		}
		stmtRef, err := p.parseSelectStmt()
		if err != nil {
			return NodeRef{}, err
		}
		if err := p.expect(lexer.KindRightParen); err != nil {
			return NodeRef{}, fmt.Errorf("EXISTS: %w", err)
		}
		sq := SubqueryExpr{ID: int32(len(p.doc.SubqueryExprs)), Stmt: stmtRef, Exists: true}
		p.doc.SubqueryExprs = append(p.doc.SubqueryExprs, sq)
		left = NodeRef{Kind: NodeKindSubqueryExpr, ID: sq.ID}

	case lexer.KindLeftParen:
		// Subquery: (SELECT ...)
		if p.next.Kind == lexer.KindSelect {
			left = p.parseSubquery()
		} else {
			p.advance() // consume (
			var err error
			left, err = p.parseExpr(0)
			if err != nil {
				return NodeRef{}, err
			}
			p.expect(lexer.KindRightParen)
		}

	case lexer.KindNot:
		p.advance()
		expr, err := p.parseExpr(operatorPrecedence(lexer.KindNot))
		if err != nil {
			return NodeRef{}, err
		}
		un := UnaryExpr{
			ID:       int32(len(p.doc.UnaryExprs)),
			Expr:     expr,
			Operator: uint8(lexer.KindNot),
		}
		p.doc.UnaryExprs = append(p.doc.UnaryExprs, un)
		left = NodeRef{Kind: NodeKindUnaryExpr, ID: un.ID}
	default:
		return NodeRef{}, fmt.Errorf("unexpected expression token: %v", p.curr.Kind)
	}

	// LED (Left Denotation)
	for {
		// PostgreSQL/SQLAlchemy JSONB subscripting (`payload['profile']`) is
		// equivalent to JSON value extraction. Lower it to the existing JSON
		// extraction binary-expression path so execution and indexes share the
		// same evaluator as `payload -> 'profile'`.
		if p.curr.Kind == lexer.KindLeftBracket {
			if operatorPrecedence(lexer.KindJSONExtract) <= precedence {
				break
			}
			p.advance()
			key, err := p.parseExpr(0)
			if err != nil {
				return NodeRef{}, fmt.Errorf("JSON subscript: %w", err)
			}
			if err := p.expect(lexer.KindRightBracket); err != nil {
				return NodeRef{}, fmt.Errorf("JSON subscript: %w", err)
			}
			be := BinaryExpr{
				ID:       int32(len(p.doc.BinaryExprs)),
				Left:     left,
				Right:    key,
				Operator: uint8(lexer.KindJSONExtract),
			}
			p.doc.BinaryExprs = append(p.doc.BinaryExprs, be)
			left = NodeRef{Kind: NodeKindBinaryExpr, ID: be.ID}
			continue
		}
		isNot := false
		// Peek for NOT IN or NOT BETWEEN
		opKind := p.curr.Kind
		if opKind == lexer.KindNot {
			if p.next.Kind == lexer.KindIn || p.next.Kind == lexer.KindBetween {
				isNot = true
				opKind = p.next.Kind
			}
		}

		prec := operatorPrecedence(opKind)
		if prec <= precedence {
			break
		}
		if opKind == lexer.KindIs {
			p.advance() // consume IS
			// SQLAlchemy renders boolean filters as `... IS true`/`IS false`.
			// Treat these null-safe boolean tests as equality against the
			// existing literal identifier representation; NULL remains false.
			if p.curr.Kind == lexer.KindIdentifier {
				literal := p.src[p.curr.Start:p.curr.End]
				if bytes.EqualFold(literal, []byte("true")) || bytes.EqualFold(literal, []byte("false")) {
					rightID := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End, ResolvedKind: ResolvedKindLiteral}
					right := NodeRef{Kind: NodeKindIdentifier, ID: rightID.ID}
					p.doc.Identifiers = append(p.doc.Identifiers, rightID)
					p.advance()
					be := BinaryExpr{ID: int32(len(p.doc.BinaryExprs)), Left: left, Right: right, Operator: uint8(lexer.KindEquals)}
					p.doc.BinaryExprs = append(p.doc.BinaryExprs, be)
					left = NodeRef{Kind: NodeKindBinaryExpr, ID: be.ID}
					continue
				}
			}
			nullTest := NullTestIsNull
			if p.curr.Kind == lexer.KindNot {
				nullTest = NullTestNotNull
				p.advance()
			}
			if p.curr.Kind != lexer.KindNull {
				return NodeRef{}, fmt.Errorf("expected NULL after IS")
			}
			p.advance()
			be := BinaryExpr{
				ID:       int32(len(p.doc.BinaryExprs)),
				Left:     left,
				Operator: uint8(lexer.KindIs),
				NullTest: nullTest,
			}
			p.doc.BinaryExprs = append(p.doc.BinaryExprs, be)
			left = NodeRef{Kind: NodeKindBinaryExpr, ID: be.ID}
			continue
		}
		if opKind == lexer.KindCast {
			p.advance()
			if p.curr.Kind != lexer.KindIdentifier && p.curr.Kind != lexer.KindKey {
				return NodeRef{}, fmt.Errorf("expected type name after ::")
			}
			cast := CastExpr{ID: int32(len(p.doc.CastExprs)), Expr: left, TypeStart: p.curr.Start, TypeEnd: p.curr.End}
			p.doc.CastExprs = append(p.doc.CastExprs, cast)
			left = NodeRef{Kind: NodeKindCastExpr, ID: cast.ID}
			p.advance()
			continue
		}

		if isNot {
			p.advance() // consume NOT
		}
		op := p.curr.Kind
		p.advance() // consume op

		switch op {
		case lexer.KindBetween:
			lower, err := p.parseExpr(operatorPrecedence(lexer.KindAnd) + 1)
			if err != nil {
				return NodeRef{}, err
			}
			if err := p.expect(lexer.KindAnd); err != nil {
				return NodeRef{}, err
			}
			upper, err := p.parseExpr(prec)
			if err != nil {
				return NodeRef{}, err
			}
			bw := BetweenExpr{
				ID:    int32(len(p.doc.BetweenExprs)),
				Expr:  left,
				Lower: lower,
				Upper: upper,
				Not:   isNot,
			}
			p.doc.BetweenExprs = append(p.doc.BetweenExprs, bw)
			left = NodeRef{Kind: NodeKindBetweenExpr, ID: bw.ID}

		case lexer.KindIn:
			inNode := InExpr{
				ID:        int32(len(p.doc.InExprs)),
				Expr:      left,
				ListStart: int32(len(p.doc.Nodes)),
				Not:       isNot,
			}
			if p.curr.Kind == lexer.KindLeftParen {
				p.advance()
				if p.curr.Kind == lexer.KindSelect {
					stmtRef, err := p.parseSelectStmt()
					if err != nil {
						return NodeRef{}, fmt.Errorf("IN subquery: %w", err)
					}
					inNode.Subquery = NodeRef{Kind: NodeKindSubqueryExpr, ID: int32(len(p.doc.SubqueryExprs))}
					inNode.HasSubquery = true
					p.doc.SubqueryExprs = append(p.doc.SubqueryExprs, SubqueryExpr{ID: inNode.Subquery.ID, Stmt: stmtRef})
				} else {
					for p.curr.Kind != lexer.KindEOF && p.curr.Kind != lexer.KindRightParen {
						listItem, err := p.parseExpr(0)
						if err != nil {
							return NodeRef{}, err
						}
						p.doc.Nodes = append(p.doc.Nodes, listItem)
						inNode.ListCount++
						if p.curr.Kind == lexer.KindComma {
							p.advance()
						} else {
							break
						}
					}
				}
				if err := p.expect(lexer.KindRightParen); err != nil {
					return NodeRef{}, err
				}
			} else {
				// Cypher permits a list-valued parameter without wrapping it in
				// parentheses: `x IN $values`. Parse one RHS expression at the
				// current precedence so a following AND/OR remains outside IN.
				paramRef, err := p.parseExpr(prec)
				if err != nil {
					return NodeRef{}, fmt.Errorf("IN parameter: %w", err)
				}
				if paramRef.Kind != NodeKindIdentifier {
					return NodeRef{}, fmt.Errorf("IN expects a list parameter")
				}
				inNode.IsParam = true
				inNode.ParamRef = paramRef
			}
			p.doc.InExprs = append(p.doc.InExprs, inNode)
			left = NodeRef{Kind: NodeKindInExpr, ID: inNode.ID}

		default:
			right, err := p.parseExpr(prec)
			if err != nil {
				return NodeRef{}, err
			}
			be := BinaryExpr{
				ID:       int32(len(p.doc.BinaryExprs)),
				Left:     left,
				Right:    right,
				Operator: uint8(op),
			}
			p.doc.BinaryExprs = append(p.doc.BinaryExprs, be)
			left = NodeRef{Kind: NodeKindBinaryExpr, ID: be.ID}
		}
	}

	return left, nil
}

func isOrderedSetAggregateName(name []byte) bool {
	return bytes.EqualFold(name, []byte("percentile_cont")) ||
		bytes.EqualFold(name, []byte("percentile_disc")) ||
		bytes.EqualFold(name, []byte("mode"))
}

func orderedSetAggregateFromName(name []byte) AggregateFunc {
	switch {
	case bytes.EqualFold(name, []byte("percentile_cont")):
		return AggPercentileCont
	case bytes.EqualFold(name, []byte("percentile_disc")):
		return AggPercentileDisc
	default:
		return AggMode
	}
}

// parseOrderedSetAggregate parses PERCENTILE_CONT/DISC and MODE using the
// PostgreSQL ordered-set form: fn(args) WITHIN GROUP (ORDER BY expr [ASC|DESC]).
// Ordered-set window usage is intentionally not accepted in this phase.
func (p *Parser) parseOrderedSetAggregate() (NodeRef, error) {
	ae := AggregateExpr{
		ID:         int32(len(p.doc.AggregateExprs)),
		Func:       orderedSetAggregateFromName(p.src[p.curr.Start:p.curr.End]),
		OrderedSet: true,
	}
	p.advance() // function name
	if err := p.expect(lexer.KindLeftParen); err != nil {
		return NodeRef{}, err
	}
	if ae.Func == AggMode {
		if p.curr.Kind != lexer.KindRightParen {
			return NodeRef{}, fmt.Errorf("MODE expects no direct arguments")
		}
	} else {
		arg, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, fmt.Errorf("ordered-set percentile argument: %w", err)
		}
		ae.Expr = arg
		if p.curr.Kind == lexer.KindComma {
			return NodeRef{}, fmt.Errorf("ordered-set percentile functions accept one direct argument")
		}
	}
	if err := p.expect(lexer.KindRightParen); err != nil {
		return NodeRef{}, err
	}
	if p.curr.Kind != lexer.KindIdentifier || !bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("within")) {
		return NodeRef{}, fmt.Errorf("expected WITHIN GROUP after ordered-set aggregate")
	}
	p.advance()
	if p.curr.Kind != lexer.KindGroup {
		return NodeRef{}, fmt.Errorf("expected GROUP after WITHIN")
	}
	p.advance()
	if err := p.expect(lexer.KindLeftParen); err != nil {
		return NodeRef{}, err
	}
	if err := p.expect(lexer.KindOrder); err != nil {
		return NodeRef{}, fmt.Errorf("expected ORDER BY in WITHIN GROUP: %w", err)
	}
	if err := p.expect(lexer.KindBy); err != nil {
		return NodeRef{}, fmt.Errorf("expected BY in WITHIN GROUP: %w", err)
	}
	orderExpr, err := p.parseExpr(0)
	if err != nil {
		return NodeRef{}, fmt.Errorf("ordered-set ORDER BY expression: %w", err)
	}
	ae.OrderExpr = orderExpr
	if p.curr.Kind == lexer.KindAsc {
		p.advance()
	} else if p.curr.Kind == lexer.KindDesc {
		ae.OrderDesc = true
		p.advance()
	}
	if err := p.expect(lexer.KindRightParen); err != nil {
		return NodeRef{}, err
	}
	p.doc.AggregateExprs = append(p.doc.AggregateExprs, ae)
	return NodeRef{Kind: NodeKindAggregateExpr, ID: ae.ID}, nil
}

// parseAggregate parses COUNT(*), COUNT(col), SUM(col), AVG(col), MIN(col),
// MAX(col), and the identifier-backed VECTOR_AVG(col).
func (p *Parser) parseAggregate() NodeRef {
	funcKind := p.curr.Kind
	funcName := p.src[p.curr.Start:p.curr.End]
	p.advance() // consume func name

	ae := AggregateExpr{
		ID:   int32(len(p.doc.AggregateExprs)),
		Func: aggregateFuncFromKind(funcKind),
	}
	if funcKind == lexer.KindIdentifier && bytes.EqualFold(funcName, []byte("vector_avg")) {
		ae.Func = AggVectorAvg
	}

	if err := p.expect(lexer.KindLeftParen); err != nil {
		// Best-effort: return zero-value ref on error; caller's problem.
		p.doc.AggregateExprs = append(p.doc.AggregateExprs, ae)
		return NodeRef{Kind: NodeKindAggregateExpr, ID: ae.ID}
	}

	// COUNT(*) special case
	if funcKind == lexer.KindCount && p.curr.Kind == lexer.KindAsterisk {
		p.advance() // consume *
		p.expect(lexer.KindRightParen)
		if p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("over")) {
			p.advance()
			if p.curr.Kind == lexer.KindLeftParen {
				window, err := p.parseWindowSpecBody()
				if err != nil {
					return NodeRef{Kind: NodeKindAggregateExpr, ID: ae.ID}
				}
				ae.WindowID, ae.HasWindow = window.ID, true
			} else if isIdentifierLike(p.curr.Kind) {
				ae.WindowNameStart, ae.WindowNameEnd = p.curr.Start, p.curr.End
				ae.WindowID, ae.HasWindow = -1, true
				p.advance()
			}
		}
		p.doc.AggregateExprs = append(p.doc.AggregateExprs, ae)
		return NodeRef{Kind: NodeKindAggregateExpr, ID: ae.ID}
	}

	// Parse every non-empty aggregate argument as an expression. Parameters,
	// literals, casts, arithmetic, and identifiers all use the same Pratt
	// parser; restricting this to identifiers leaves a parameter such as
	// MIN($threshold) unconsumed and causes the root parser to report the
	// parameter token as unexpected.
	if p.curr.Kind != lexer.KindRightParen {
		expr, err := p.parseExpr(0)
		if err == nil {
			ae.Expr = expr
		}
	}

	p.expect(lexer.KindRightParen)
	if p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("over")) {
		p.advance()
		if p.curr.Kind == lexer.KindLeftParen {
			window, err := p.parseWindowSpecBody()
			if err == nil {
				ae.WindowID, ae.HasWindow = window.ID, true
			}
		} else if isIdentifierLike(p.curr.Kind) {
			ae.WindowNameStart, ae.WindowNameEnd = p.curr.Start, p.curr.End
			ae.WindowID, ae.HasWindow = -1, true
			p.advance()
		}
	}
	p.doc.AggregateExprs = append(p.doc.AggregateExprs, ae)
	return NodeRef{Kind: NodeKindAggregateExpr, ID: ae.ID}
}

func (p *Parser) parseFunctionExpr() (NodeRef, error) {
	// Reserve the function arena slot before parsing arguments. Nested calls
	// such as RRF(..., FTS_RANK(...)) would otherwise both observe the same
	// len(FunctionExprs) and receive ID 0, causing the outer AST node to point
	// at the inner function during lowering.
	fn := FunctionExpr{ID: int32(len(p.doc.FunctionExprs)), NameStart: p.curr.Start, NameEnd: p.curr.End}
	fnID := fn.ID
	p.doc.FunctionExprs = append(p.doc.FunctionExprs, fn)
	scratchStart := len(p.functionArgsScratch)
	p.advance()
	if err := p.expect(lexer.KindLeftParen); err != nil {
		return NodeRef{}, err
	}
	if p.curr.Kind != lexer.KindRightParen {
		for {
			arg, err := p.parseExpr(0)
			if err != nil {
				return NodeRef{}, err
			}
			p.functionArgsScratch = append(p.functionArgsScratch, arg)
			fn.ArgsCount++
			if p.curr.Kind != lexer.KindComma {
				break
			}
			p.advance()
		}
	}
	if err := p.expect(lexer.KindRightParen); err != nil {
		return NodeRef{}, err
	}
	if p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("over")) {
		p.advance() // OVER
		if p.curr.Kind == lexer.KindLeftParen {
			window, err := p.parseWindowSpecBody()
			if err != nil {
				return NodeRef{}, err
			}
			fn.WindowID = window.ID
			fn.HasWindow = true
		} else if isIdentifierLike(p.curr.Kind) {
			fn.WindowNameStart, fn.WindowNameEnd = p.curr.Start, p.curr.End
			fn.WindowID = -1
			fn.HasWindow = true
			p.advance()
		} else {
			return NodeRef{}, fmt.Errorf("OVER expects a window specification or name")
		}
	}
	fn.ArgsStart = int32(len(p.doc.FunctionArgs))
	p.doc.FunctionArgs = append(p.doc.FunctionArgs, p.functionArgsScratch[scratchStart:]...)
	p.functionArgsScratch = p.functionArgsScratch[:scratchStart]
	p.doc.FunctionExprs[fnID] = fn
	return NodeRef{Kind: NodeKindFunctionExpr, ID: fnID}, nil
}

// parseCastFunctionExpr parses the function-form CAST(value AS type), which
// SQLAlchemy emits for JSON boolean extraction and other typed expressions.
// The ::type form continues to use the Pratt cast operator above.
func (p *Parser) parseCastFunctionExpr() (NodeRef, error) {
	p.advance() // CAST
	if err := p.expect(lexer.KindLeftParen); err != nil {
		return NodeRef{}, err
	}
	expr, err := p.parseExpr(0)
	if err != nil {
		return NodeRef{}, err
	}
	if err := p.expect(lexer.KindAs); err != nil {
		return NodeRef{}, fmt.Errorf("CAST expects AS: %w", err)
	}
	if p.curr.Kind != lexer.KindIdentifier && p.curr.Kind != lexer.KindKey {
		return NodeRef{}, fmt.Errorf("CAST expects a type name, got %v", p.curr.Kind)
	}
	cast := CastExpr{ID: int32(len(p.doc.CastExprs)), Expr: expr, TypeStart: p.curr.Start, TypeEnd: p.curr.End}
	p.doc.CastExprs = append(p.doc.CastExprs, cast)
	p.advance()
	if err := p.expect(lexer.KindRightParen); err != nil {
		return NodeRef{}, err
	}
	return NodeRef{Kind: NodeKindCastExpr, ID: cast.ID}, nil
}

// parseWindowSpec parses a window specification:
// OVER (PARTITION BY expr [, ...] ORDER BY expr [ASC|DESC] [, ...]
//
//	[ROWS|RANGE frame]).
func (p *Parser) parseWindowSpec() (WindowSpec, error) {
	p.advance() // OVER
	return p.parseWindowSpecBody()
}

func (p *Parser) parseWindowSpecBody() (WindowSpec, error) {
	if err := p.expect(lexer.KindLeftParen); err != nil {
		return WindowSpec{}, fmt.Errorf("OVER: %w", err)
	}
	w := WindowSpec{ID: int32(len(p.doc.WindowSpecs))}
	if p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("partition")) {
		p.advance()
		if err := p.expect(lexer.KindBy); err != nil {
			return WindowSpec{}, fmt.Errorf("PARTITION: %w", err)
		}
		w.PartitionStart = int32(len(p.doc.Nodes))
		for {
			expr, err := p.parseExpr(0)
			if err != nil {
				return WindowSpec{}, fmt.Errorf("PARTITION BY: %w", err)
			}
			p.doc.Nodes = append(p.doc.Nodes, expr)
			w.PartitionCount++
			if p.curr.Kind != lexer.KindComma {
				break
			}
			p.advance()
		}
	}
	if p.curr.Kind == lexer.KindOrder {
		p.advance()
		if err := p.expect(lexer.KindBy); err != nil {
			return WindowSpec{}, fmt.Errorf("window ORDER: %w", err)
		}
		w.OrderStart = int32(len(p.doc.WindowOrders))
		for {
			order, err := p.parseExpr(0)
			if err != nil {
				return WindowSpec{}, fmt.Errorf("window ORDER BY: %w", err)
			}
			item := WindowOrder{Expr: order}
			if p.curr.Kind == lexer.KindDesc {
				item.IsDesc = true
				p.advance()
			} else if p.curr.Kind == lexer.KindAsc {
				p.advance()
			}
			if p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("nulls")) {
				p.advance()
				if p.curr.Kind != lexer.KindIdentifier {
					return WindowSpec{}, fmt.Errorf("window ORDER NULLS expects FIRST or LAST")
				}
				switch {
				case bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("first")):
					item.NullsOrder = WindowNullsFirst
				case bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("last")):
					item.NullsOrder = WindowNullsLast
				default:
					return WindowSpec{}, fmt.Errorf("window ORDER NULLS expects FIRST or LAST")
				}
				p.advance()
			}
			p.doc.WindowOrders = append(p.doc.WindowOrders, item)
			w.OrderCount++
			if w.OrderCount == 1 {
				w.OrderBy = order
				w.IsDesc = item.IsDesc
			}
			if p.curr.Kind != lexer.KindComma {
				break
			}
			p.advance()
		}
	}
	if p.curr.Kind == lexer.KindIdentifier && (bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("rows")) || bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("range"))) {
		w.Frame.HasFrame = true
		w.Frame.IsRange = bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("range"))
		p.advance()
		if p.curr.Kind == lexer.KindBetween {
			p.advance()
			start, err := p.parseWindowFrameBound()
			if err != nil {
				return WindowSpec{}, fmt.Errorf("window frame start: %w", err)
			}
			if err := p.expect(lexer.KindAnd); err != nil {
				return WindowSpec{}, fmt.Errorf("window frame BETWEEN: %w", err)
			}
			end, err := p.parseWindowFrameBound()
			if err != nil {
				return WindowSpec{}, fmt.Errorf("window frame end: %w", err)
			}
			w.Frame.Start, w.Frame.End = start, end
		} else {
			start, err := p.parseWindowFrameBound()
			if err != nil {
				return WindowSpec{}, fmt.Errorf("window frame: %w", err)
			}
			w.Frame.Start = start
			w.Frame.End = WindowFrameBound{Kind: WindowFrameCurrentRow}
		}
	}
	if err := p.expect(lexer.KindRightParen); err != nil {
		return WindowSpec{}, fmt.Errorf("OVER: %w", err)
	}
	p.doc.WindowSpecs = append(p.doc.WindowSpecs, w)
	return w, nil
}

func (p *Parser) parseWindowFrameBound() (WindowFrameBound, error) {
	if p.curr.Kind == lexer.KindIdentifier {
		text := p.src[p.curr.Start:p.curr.End]
		if bytes.EqualFold(text, []byte("unbounded")) {
			p.advance()
			if p.curr.Kind != lexer.KindIdentifier {
				return WindowFrameBound{}, fmt.Errorf("expected PRECEDING or FOLLOWING after UNBOUNDED")
			}
			direction := p.src[p.curr.Start:p.curr.End]
			if bytes.EqualFold(direction, []byte("preceding")) {
				p.advance()
				return WindowFrameBound{Kind: WindowFrameUnboundedPreceding}, nil
			}
			if bytes.EqualFold(direction, []byte("following")) {
				p.advance()
				return WindowFrameBound{Kind: WindowFrameUnboundedFollowing}, nil
			}
			return WindowFrameBound{}, fmt.Errorf("expected PRECEDING or FOLLOWING after UNBOUNDED")
		}
		if bytes.EqualFold(text, []byte("current")) {
			p.advance()
			if p.curr.Kind != lexer.KindIdentifier || !bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("row")) {
				return WindowFrameBound{}, fmt.Errorf("expected ROW after CURRENT")
			}
			p.advance()
			return WindowFrameBound{Kind: WindowFrameCurrentRow}, nil
		}
	}
	offset, err := p.parseExpr(0)
	if err != nil {
		return WindowFrameBound{}, err
	}
	if p.curr.Kind != lexer.KindIdentifier {
		return WindowFrameBound{}, fmt.Errorf("expected PRECEDING or FOLLOWING")
	}
	direction := p.src[p.curr.Start:p.curr.End]
	if bytes.EqualFold(direction, []byte("preceding")) {
		p.advance()
		return WindowFrameBound{Kind: WindowFramePreceding, Expr: offset}, nil
	}
	if bytes.EqualFold(direction, []byte("following")) {
		p.advance()
		return WindowFrameBound{Kind: WindowFrameFollowing, Expr: offset}, nil
	}
	return WindowFrameBound{}, fmt.Errorf("expected PRECEDING or FOLLOWING")
}

func (p *Parser) parseCaseExpr() (NodeRef, error) {
	p.advance() // CASE
	ce := CaseExpr{ID: int32(len(p.doc.CaseExprs)), WhensStart: int32(len(p.doc.CaseWhens))}
	for p.curr.Kind == lexer.KindWhen {
		p.advance()
		condition, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		if err := p.expect(lexer.KindThen); err != nil {
			return NodeRef{}, err
		}
		value, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		p.doc.CaseWhens = append(p.doc.CaseWhens, CaseWhen{Condition: condition, Value: value})
		ce.WhensCount++
	}
	if ce.WhensCount == 0 {
		return NodeRef{}, errors.New("CASE requires at least one WHEN clause")
	}
	if p.curr.Kind == lexer.KindElse {
		p.advance()
		value, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		ce.Else = value
		ce.HasElse = true
	}
	if err := p.expect(lexer.KindEnd); err != nil {
		return NodeRef{}, err
	}
	p.doc.CaseExprs = append(p.doc.CaseExprs, ce)
	return NodeRef{Kind: NodeKindCaseExpr, ID: ce.ID}, nil
}

func aggregateFuncFromKind(k lexer.Kind) AggregateFunc {
	switch k {
	case lexer.KindCount:
		return AggCount
	case lexer.KindSum:
		return AggSum
	case lexer.KindAvg:
		return AggAvg
	case lexer.KindMin:
		return AggMin
	case lexer.KindMax:
		return AggMax
	default:
		return AggCount
	}
}

// parseUint16 consumes the current token as a number and returns its uint16 value.
// Returns 0 if the current token is not a number.
func (p *Parser) parseUint16() uint16 {
	if p.curr.Kind != lexer.KindNumber {
		return 0
	}
	tok := p.curr
	p.advance()
	var v uint16
	for i := tok.Start; i < tok.End; i++ {
		v = v*10 + uint16(p.src[i]-'0')
	}
	return v
}

func operatorPrecedence(kind lexer.Kind) int {
	switch kind {
	case lexer.KindOr:
		return 1
	case lexer.KindAnd:
		return 2
	case lexer.KindNot:
		return 3
	case lexer.KindEquals, lexer.KindGreaterThan, lexer.KindLessThan, lexer.KindBetween, lexer.KindIn,
		lexer.KindGreaterEqual, lexer.KindLessEqual, lexer.KindNotEqual,
		lexer.KindL2Dist, lexer.KindIPDist, lexer.KindCosineDist, lexer.KindIs,
		lexer.KindLike, lexer.KindILike, lexer.KindFTSMatch,
		lexer.KindJSONContains, lexer.KindJSONContainedBy, lexer.KindJSONExists,
		lexer.KindJSONAny, lexer.KindJSONAll, lexer.KindJSONPathExists,
		lexer.KindJSONDelete:
		return 4
	case lexer.KindArrowRight, lexer.KindJSONExtract, lexer.KindJSONExtractText,
		lexer.KindJSONPath, lexer.KindJSONPathText:
		return 7
	case lexer.KindPlus, lexer.KindDash, lexer.KindConcat:
		return 5 // addition/subtraction
	case lexer.KindAsterisk, lexer.KindSlash, lexer.KindPercent:
		return 6 // multiplication
	case lexer.KindShiftLeft, lexer.KindShiftRight:
		return 5
	case lexer.KindCast:
		return 7
	}
	return 0
}

// dispatchInsertStmt peeks at the table name after INSERT INTO to decide
// whether to parse a standard record INSERT or a graph edge INSERT.
func (p *Parser) dispatchInsertStmt() error {
	p.advance() // consume INSERT
	if err := p.expect(lexer.KindInto); err != nil {
		return err
	}

	if p.curr.Kind != lexer.KindIdentifier {
		return fmt.Errorf("expected table name after INSERT INTO")
	}

	// Peek at the identifier to check for GRAPH_EDGES.
	if p.curr.End > p.curr.Start && string(p.src[p.curr.Start:p.curr.End]) == "GRAPH_EDGES" {
		p.advance() // consume GRAPH_EDGES identifier
		return p.parseInsertGraphEdgeStmt()
	}

	// Standard INSERT: delegate to existing parser which expects the table
	// name as the current token.
	return p.parseInsertStmt()
}

func (p *Parser) parseInsertStmt() error {
	// NOTE: Called from dispatchInsertStmt which has already consumed INSERT and INTO.
	// The current token is the table name identifier.

	stmt := InsertStmt{}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.TableStart = p.curr.Start
		stmt.TableEnd = p.curr.End
		p.advance()
	} else {
		return fmt.Errorf("expected table name after INSERT INTO")
	}

	// Optional column list: (col1, col2)
	if p.curr.Kind == lexer.KindLeftParen {
		p.advance()
		for p.curr.Kind != lexer.KindRightParen && p.curr.Kind != lexer.KindEOF {
			if isColumnToken(p.curr.Kind) {
				id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
				p.doc.Identifiers = append(p.doc.Identifiers, id)
				stmt.Columns = append(stmt.Columns, NodeRef{Kind: NodeKindIdentifier, ID: id.ID})
				p.advance()
			}
			if p.curr.Kind == lexer.KindComma {
				p.advance()
			}
		}
		p.expect(lexer.KindRightParen)
	}

	if p.curr.Kind == lexer.KindSelect {
		selectStart := p.curr.Start
		selectRef, err := p.parseSelectStmt()
		if err != nil {
			return fmt.Errorf("INSERT ... SELECT: %w", err)
		}
		stmt.Select = selectRef
		stmt.HasSelect = true
		stmt.SelectStart = selectStart
		stmt.SelectEnd = p.curr.Start
		if stmt.SelectEnd == 0 || stmt.SelectEnd > uint32(len(p.src)) {
			stmt.SelectEnd = uint32(len(p.src))
		}
	} else {
		// VALUES (val1, val2), (val3, val4), ...
		if p.curr.Kind == lexer.KindValues {
			p.advance()
		}
		// Parse one or more tuple groups: (a, b, c), (d, e, f), ...
		for p.curr.Kind == lexer.KindLeftParen {
			p.advance() // consume '('
			for p.curr.Kind != lexer.KindRightParen && p.curr.Kind != lexer.KindEOF {
				if p.curr.Kind == lexer.KindError {
					return fmt.Errorf("invalid token in INSERT VALUES at %d", p.curr.Start)
				}
				switch p.curr.Kind {
				case lexer.KindString, lexer.KindEscapeString:
					sl := StringLiteral{ID: int32(len(p.doc.Strings)), Start: p.curr.Start, End: p.curr.End, Escape: p.curr.Kind == lexer.KindEscapeString}
					p.doc.Strings = append(p.doc.Strings, sl)
					stmt.Values = append(stmt.Values, NodeRef{Kind: NodeKindString, ID: sl.ID})
					p.advance()
				case lexer.KindParam:
					id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
					p.doc.Identifiers = append(p.doc.Identifiers, id)
					stmt.Values = append(stmt.Values, NodeRef{Kind: NodeKindIdentifier, ID: id.ID})
					p.advance()
				case lexer.KindPlus, lexer.KindDash:
					// Signed number: +/-<digits>. Emit one Number node spanning
					// the sign and digits so the optimizer sees the exact literal.
					dashStart := p.curr.Start
					p.advance()
					if p.curr.Kind == lexer.KindNumber {
						num := Number{ID: int32(len(p.doc.Numbers)), Start: dashStart, End: p.curr.End}
						p.doc.Numbers = append(p.doc.Numbers, num)
						stmt.Values = append(stmt.Values, NodeRef{Kind: NodeKindNumber, ID: num.ID})
						p.advance()
					}
				case lexer.KindNumber:
					num := Number{ID: int32(len(p.doc.Numbers)), Start: p.curr.Start, End: p.curr.End}
					p.doc.Numbers = append(p.doc.Numbers, num)
					stmt.Values = append(stmt.Values, NodeRef{Kind: NodeKindNumber, ID: num.ID})
					p.advance()
				case lexer.KindNull:
					// Preserve explicit SQL NULL so lowering can distinguish it
					// from an omitted column and enforce NOT NULL correctly.
					id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End, ResolvedKind: ResolvedKindLiteral}
					p.doc.Identifiers = append(p.doc.Identifiers, id)
					stmt.Values = append(stmt.Values, NodeRef{Kind: NodeKindIdentifier, ID: id.ID})
					p.advance()
				case lexer.KindIdentifier:
					// TRUE and FALSE are identifier tokens in the shared lexer.
					// They are still SQL literals in VALUES; dropping them would
					// shift every following value one column to the left.
					literal := p.src[p.curr.Start:p.curr.End]
					if bytes.EqualFold(literal, []byte("cast")) && p.next.Kind == lexer.KindLeftParen {
						p.advance() // CAST
						p.advance() // '('
						value, err := p.parseDMLValueRef()
						if err != nil {
							return fmt.Errorf("INSERT CAST value: %w", err)
						}
						if err := p.expect(lexer.KindAs); err != nil {
							return fmt.Errorf("INSERT CAST value: %w", err)
						}
						if p.curr.Kind != lexer.KindIdentifier && p.curr.Kind != lexer.KindKey {
							return fmt.Errorf("INSERT CAST value: expected type name, got %v", p.curr.Kind)
						}
						p.advance()
						if err := p.expect(lexer.KindRightParen); err != nil {
							return fmt.Errorf("INSERT CAST value: %w", err)
						}
						stmt.Values = append(stmt.Values, value)
						break
					}
					if !bytes.EqualFold(literal, []byte("true")) && !bytes.EqualFold(literal, []byte("false")) {
						return fmt.Errorf("unsupported identifier in INSERT VALUES at %d", p.curr.Start)
					}
					id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End, ResolvedKind: ResolvedKindLiteral}
					p.doc.Identifiers = append(p.doc.Identifiers, id)
					stmt.Values = append(stmt.Values, NodeRef{Kind: NodeKindIdentifier, ID: id.ID})
					p.advance()
				case lexer.KindCast:
					// A postfix cast such as $1::jsonb does not change the
					// storage literal here; the target column's catalog type
					// performs validation and canonicalization.
					p.advance()
					if p.curr.Kind != lexer.KindIdentifier && p.curr.Kind != lexer.KindKey {
						return fmt.Errorf("INSERT postfix cast: expected type name, got %v", p.curr.Kind)
					}
					p.advance()
				default:
					return fmt.Errorf("unsupported token in INSERT VALUES at %d: %v", p.curr.Start, p.curr.Kind)
				}
				if p.curr.Kind == lexer.KindComma {
					p.advance()
				}
			}
			if err := p.expect(lexer.KindRightParen); err != nil {
				return fmt.Errorf("expected ')' after INSERT VALUES: %w", err)
			}
			// Skip any comma separating this tuple from the next
			if p.curr.Kind == lexer.KindComma {
				p.advance()
			}
		}
	}

	// PostgreSQL-compatible upsert tail. Parse it here, rather than allowing
	// the root parser to silently ignore trailing tokens after INSERT.
	if p.curr.Kind == lexer.KindOn {
		if err := p.parseInsertConflict(&stmt); err != nil {
			return err
		}
		if p.curr.Kind != lexer.KindEOF && p.curr.Kind != lexer.KindReturning {
			if p.curr.Kind == lexer.KindError && p.curr.Start < uint32(len(p.src)) && p.src[p.curr.Start] == ';' {
				p.advance()
			} else {
				return fmt.Errorf("unexpected token after INSERT ... ON CONFLICT: %v", p.curr.Kind)
			}
		}
	}
	if p.curr.Kind == lexer.KindReturning {
		if err := p.parseReturning(&stmt.Returning, &stmt.ReturningStar); err != nil {
			return err
		}
	}

	p.doc.InsertStmts = append(p.doc.InsertStmts, stmt)
	return nil
}

// parseInsertConflict parses:
//
//	ON CONFLICT [(column [, ...])] DO NOTHING
//	ON CONFLICT [(column [, ...])] DO UPDATE SET column = value [, ...]
//
// The parser preserves EXCLUDED.column as a dedicated AST reference so the
// executor can distinguish proposed-row values from ordinary literals.
func (p *Parser) parseInsertConflict(stmt *InsertStmt) error {
	if stmt == nil {
		return errors.New("INSERT conflict target has nil statement")
	}
	p.advance() // ON
	if err := p.expect(lexer.KindConflict); err != nil {
		return fmt.Errorf("expected CONFLICT after ON: %w", err)
	}
	if p.curr.Kind == lexer.KindOn {
		p.advance()
		if err := p.expect(lexer.KindConstraint); err != nil {
			return fmt.Errorf("expected CONSTRAINT after ON CONFLICT ON: %w", err)
		}
		if !isColumnToken(p.curr.Kind) {
			return fmt.Errorf("expected constraint name after ON CONFLICT ON CONSTRAINT")
		}
		stmt.ConflictConstraintStart = p.curr.Start
		stmt.ConflictConstraintEnd = p.curr.End
		p.advance()
	} else if p.curr.Kind == lexer.KindLeftParen {
		p.advance()
		if p.curr.Kind == lexer.KindRightParen {
			return errors.New("ON CONFLICT target cannot be empty")
		}
		for {
			if !isColumnToken(p.curr.Kind) {
				return fmt.Errorf("expected conflict target column, got %v", p.curr.Kind)
			}
			id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
			p.doc.Identifiers = append(p.doc.Identifiers, id)
			stmt.ConflictColumns = append(stmt.ConflictColumns, NodeRef{Kind: NodeKindIdentifier, ID: id.ID})
			p.advance()
			if p.curr.Kind != lexer.KindComma {
				break
			}
			p.advance()
		}
		if err := p.expect(lexer.KindRightParen); err != nil {
			return fmt.Errorf("expected ')' after ON CONFLICT target: %w", err)
		}
	}
	if err := p.expect(lexer.KindDo); err != nil {
		return fmt.Errorf("expected DO after ON CONFLICT: %w", err)
	}
	if p.curr.Kind == lexer.KindNothing {
		stmt.ConflictAction = 1
		p.advance()
		if p.curr.Kind == lexer.KindWhere {
			return errors.New("ON CONFLICT DO NOTHING cannot have a WHERE clause")
		}
		return nil
	}
	if err := p.expect(lexer.KindUpdate); err != nil {
		return fmt.Errorf("expected NOTHING or UPDATE after ON CONFLICT DO: %w", err)
	}
	if err := p.expect(lexer.KindSet); err != nil {
		return fmt.Errorf("expected SET after ON CONFLICT DO UPDATE: %w", err)
	}
	stmt.ConflictAction = 2
	seen := make(map[string]struct{})
	for {
		if !isColumnToken(p.curr.Kind) {
			return fmt.Errorf("expected column in ON CONFLICT DO UPDATE SET, got %v", p.curr.Kind)
		}
		columnText := string(p.src[p.curr.Start:p.curr.End])
		column := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
		p.doc.Identifiers = append(p.doc.Identifiers, column)
		assignment := InsertConflictAssignment{Column: NodeRef{Kind: NodeKindIdentifier, ID: column.ID}}
		key := toLower(columnText)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate ON CONFLICT assignment column %q", columnText)
		}
		seen[key] = struct{}{}
		p.advance()
		if err := p.expect(lexer.KindEquals); err != nil {
			return fmt.Errorf("expected '=' after ON CONFLICT assignment column %q: %w", columnText, err)
		}
		value, err := p.parseExpr(0)
		if err != nil {
			return fmt.Errorf("ON CONFLICT assignment %q: %w", columnText, err)
		}
		assignment.Value = value
		if value.Kind == NodeKindIdentifier && value.ID >= 0 && int(value.ID) < len(p.doc.Identifiers) &&
			p.doc.Identifiers[value.ID].ResolvedKind == ResolvedKindExcluded {
			assignment.ExcludedColumn = value
		}
		stmt.ConflictSet = append(stmt.ConflictSet, assignment)
		if p.curr.Kind != lexer.KindComma {
			break
		}
		p.advance()
	}
	if p.curr.Kind == lexer.KindWhere {
		p.advance()
		where, err := p.parseExpr(0)
		if err != nil {
			return fmt.Errorf("ON CONFLICT DO UPDATE WHERE: %w", err)
		}
		stmt.ConflictWhere = where
		stmt.HasConflictWhere = true
	}
	return nil
}

func (p *Parser) parseDMLValueRef() (NodeRef, error) {
	switch p.curr.Kind {
	case lexer.KindString, lexer.KindEscapeString:
		sl := StringLiteral{ID: int32(len(p.doc.Strings)), Start: p.curr.Start, End: p.curr.End, Escape: p.curr.Kind == lexer.KindEscapeString}
		p.doc.Strings = append(p.doc.Strings, sl)
		p.advance()
		return NodeRef{Kind: NodeKindString, ID: sl.ID}, nil
	case lexer.KindNumber:
		num := Number{ID: int32(len(p.doc.Numbers)), Start: p.curr.Start, End: p.curr.End}
		p.doc.Numbers = append(p.doc.Numbers, num)
		p.advance()
		return NodeRef{Kind: NodeKindNumber, ID: num.ID}, nil
	case lexer.KindParam:
		id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		p.advance()
		return NodeRef{Kind: NodeKindIdentifier, ID: id.ID}, nil
	case lexer.KindNull:
		id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End, ResolvedKind: ResolvedKindLiteral}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		p.advance()
		return NodeRef{Kind: NodeKindIdentifier, ID: id.ID}, nil
	case lexer.KindIdentifier:
		literal := p.src[p.curr.Start:p.curr.End]
		if !bytes.EqualFold(literal, []byte("true")) && !bytes.EqualFold(literal, []byte("false")) {
			return NodeRef{}, fmt.Errorf("expected literal, parameter, or EXCLUDED.column, got %v", p.curr.Kind)
		}
		id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End, ResolvedKind: ResolvedKindLiteral}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		p.advance()
		return NodeRef{Kind: NodeKindIdentifier, ID: id.ID}, nil
	case lexer.KindDash:
		start := p.curr.Start
		p.advance()
		if p.curr.Kind != lexer.KindNumber {
			return NodeRef{}, errors.New("expected number after '-' in value")
		}
		num := Number{ID: int32(len(p.doc.Numbers)), Start: start, End: p.curr.End}
		p.doc.Numbers = append(p.doc.Numbers, num)
		p.advance()
		return NodeRef{Kind: NodeKindNumber, ID: num.ID}, nil
	default:
		return NodeRef{}, fmt.Errorf("expected literal, parameter, or EXCLUDED.column, got %v", p.curr.Kind)
	}
}

// parseInsertGraphEdgeStmt parses INSERT INTO GRAPH_EDGES VALUES
// (src, edge_kind, tgt [, properties]). The optional column list accepts
// source, type/edge_kind, target, and properties in any order.
// The GRAPH_EDGES identifier has already been consumed by dispatchInsertStmt.
func (p *Parser) parseInsertGraphEdgeStmt() error {
	columns := []string{"source", "type", "target", "properties"}
	explicitColumns := false
	if p.curr.Kind == lexer.KindLeftParen {
		explicitColumns = true
		p.advance()
		columns = columns[:0]
		for p.curr.Kind != lexer.KindRightParen && p.curr.Kind != lexer.KindEOF {
			if p.curr.Kind != lexer.KindIdentifier {
				return fmt.Errorf("expected GRAPH_EDGES column name, got %v", p.curr.Kind)
			}
			columns = append(columns, strings.ToLower(string(p.src[p.curr.Start:p.curr.End])))
			p.advance()
			if p.curr.Kind == lexer.KindComma {
				p.advance()
			}
		}
		if err := p.expect(lexer.KindRightParen); err != nil {
			return err
		}
		if len(columns) < 3 || len(columns) > 4 {
			return fmt.Errorf("GRAPH_EDGES column list must contain source, type, target, and optional properties")
		}
	}
	// Consume optional VALUES keyword.
	if p.curr.Kind == lexer.KindValues {
		p.advance()
	}

	// Parse one or more tuple groups: (src, kind, tgt), ...
	for p.curr.Kind == lexer.KindLeftParen {
		p.advance() // consume '('

		stmt := InsertGraphEdgeStmt{}
		field := 0
		for p.curr.Kind != lexer.KindRightParen && p.curr.Kind != lexer.KindEOF {
			if field >= len(columns) {
				return fmt.Errorf("too many values in GRAPH_EDGES VALUES tuple (expected %d values)", len(columns))
			}
			value, err := p.parseDMLValueRef()
			if err != nil {
				return fmt.Errorf("GRAPH_EDGES VALUES tuple: %w", err)
			}
			column := strings.ToLower(columns[field])
			switch column {
			case "source", "src":
				stmt.SrcExpr = value
				if value.Kind == NodeKindString {
					literal := p.doc.Strings[value.ID]
					stmt.SrcStart = literal.Start + 1 // skip opening quote
					stmt.SrcEnd = literal.End - 1     // skip closing quote
				}
			case "type", "kind", "edge_kind":
				stmt.EdgeKindExpr = value
				if value.Kind == NodeKindString {
					literal := p.doc.Strings[value.ID]
					stmt.EdgeKindStart = literal.Start + 1
					stmt.EdgeKindEnd = literal.End - 1
				}
			case "target", "tgt":
				stmt.TgtExpr = value
				if value.Kind == NodeKindString {
					literal := p.doc.Strings[value.ID]
					stmt.TgtStart = literal.Start + 1
					stmt.TgtEnd = literal.End - 1
				}
			case "properties", "property":
				stmt.PropertiesExpr = value
				if value.Kind != NodeKindString {
					return fmt.Errorf("GRAPH_EDGES properties must be a JSON string literal")
				}
				literal := p.doc.Strings[value.ID]
				stmt.PropertiesStart = literal.Start + 1
				stmt.PropertiesEnd = literal.End - 1
				stmt.HasProperties = true
			default:
				return fmt.Errorf("unsupported GRAPH_EDGES column %q", columns[field])
			}
			field++
			p.advance()

			if p.curr.Kind == lexer.KindComma {
				p.advance()
			}
		}
		if (explicitColumns && field != len(columns)) || (!explicitColumns && field != 3 && field != 4) {
			return fmt.Errorf("GRAPH_EDGES VALUES tuple must have exactly %d values, got %d", len(columns), field)
		}
		p.expect(lexer.KindRightParen)

		p.doc.InsertGraphEdgeStmts = append(p.doc.InsertGraphEdgeStmts, stmt)

		// Skip any comma separating this tuple from the next.
		if p.curr.Kind == lexer.KindComma {
			p.advance()
		}
	}
	return nil
}

func (p *Parser) parseUpdateStmt() error {
	p.advance() // consume UPDATE

	stmt := UpdateStmt{}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.TableStart = p.curr.Start
		stmt.TableEnd = p.curr.End
		p.advance()
	}

	// SET col1 = val1, col2 = val2
	if p.curr.Kind == lexer.KindSet {
		p.advance()
		for p.curr.Kind != lexer.KindWhere && p.curr.Kind != lexer.KindReturning && p.curr.Kind != lexer.KindEOF {
			if p.curr.Kind == lexer.KindIdentifier || p.curr.Kind == lexer.KindKey {
				id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
				p.doc.Identifiers = append(p.doc.Identifiers, id)
				stmt.SetColumns = append(stmt.SetColumns, NodeRef{Kind: NodeKindIdentifier, ID: id.ID})
				p.advance()
			} else {
				return fmt.Errorf("expected column name in UPDATE SET, got %v", p.curr.Kind)
			}
			if err := p.expect(lexer.KindEquals); err != nil {
				return err
			}
			value, err := p.parseExpr(0)
			if err != nil {
				return fmt.Errorf("UPDATE SET %w", err)
			}
			stmt.SetValues = append(stmt.SetValues, value)
			if p.curr.Kind == lexer.KindComma {
				p.advance()
				continue
			}
			if p.curr.Kind != lexer.KindWhere && p.curr.Kind != lexer.KindEOF {
				return fmt.Errorf("expected comma or WHERE after UPDATE assignment, got %v", p.curr.Kind)
			}
		}
	}

	// Optional WHERE
	if p.curr.Kind == lexer.KindWhere {
		p.advance()
		whereRef, err := p.parseExpr(0)
		if err != nil {
			return err
		}
		stmt.WhereExpr = whereRef
	}
	if p.curr.Kind == lexer.KindReturning {
		if err := p.parseReturning(&stmt.Returning, &stmt.ReturningStar); err != nil {
			return err
		}
	}

	p.doc.UpdateStmts = append(p.doc.UpdateStmts, stmt)
	return nil
}

func (p *Parser) parseDeleteStmt() error {
	p.advance() // consume DELETE
	if p.curr.Kind == lexer.KindFrom {
		p.advance()
	}

	stmt := DeleteStmt{}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.TableStart = p.curr.Start
		stmt.TableEnd = p.curr.End
		p.advance()
	}

	// Optional WHERE
	if p.curr.Kind == lexer.KindWhere {
		p.advance()
		whereRef, err := p.parseExpr(0)
		if err != nil {
			return err
		}
		stmt.WhereExpr = whereRef
	}
	if p.curr.Kind == lexer.KindReturning {
		if err := p.parseReturning(&stmt.Returning, &stmt.ReturningStar); err != nil {
			return err
		}
	}

	p.doc.DeleteStmts = append(p.doc.DeleteStmts, stmt)
	return nil
}

// parseReturning parses the restricted, allocation-free DML RETURNING
// projection used by the executor: RETURNING * or a comma-separated list of
// column identifiers. Expression projections can be added without changing
// the DML execution contract; ordinary column lists cover driver and ORM
// insert/update/delete workflows.
func (p *Parser) parseReturning(columns *[]NodeRef, star *bool) error {
	if columns == nil || star == nil {
		return fmt.Errorf("invalid RETURNING destination")
	}
	p.advance() // consume RETURNING
	if p.curr.Kind == lexer.KindAsterisk {
		*star = true
		p.advance()
		return nil
	}
	if !isColumnToken(p.curr.Kind) {
		return fmt.Errorf("expected column or '*' after RETURNING, got %v", p.curr.Kind)
	}
	for {
		if !isColumnToken(p.curr.Kind) {
			return fmt.Errorf("expected column in RETURNING list, got %v", p.curr.Kind)
		}
		start := p.curr.Start
		end := p.curr.End
		p.advance()
		for p.curr.Kind == lexer.KindDot {
			p.advance()
			if !isColumnToken(p.curr.Kind) {
				return fmt.Errorf("expected column after '.' in RETURNING list")
			}
			start = p.curr.Start
			end = p.curr.End
			p.advance()
		}
		id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: start, End: end}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		*columns = append(*columns, NodeRef{Kind: NodeKindIdentifier, ID: id.ID})
		if p.curr.Kind != lexer.KindComma {
			break
		}
		p.advance()
		if !isColumnToken(p.curr.Kind) {
			return fmt.Errorf("expected column after comma in RETURNING list")
		}
	}
	return nil
}

// parseSubquery parses a parenthesized SELECT subquery: (SELECT ...)
func (p *Parser) parseSubquery() NodeRef {
	p.advance() // consume (
	stmtRef, err := p.parseSelectStmt()
	if err != nil {
		// Best-effort: return a zero-value ref
		return NodeRef{}
	}
	p.expect(lexer.KindRightParen)

	sq := SubqueryExpr{
		ID:   int32(len(p.doc.SubqueryExprs)),
		Stmt: stmtRef,
	}
	p.doc.SubqueryExprs = append(p.doc.SubqueryExprs, sq)
	return NodeRef{Kind: NodeKindSubqueryExpr, ID: sq.ID}
}

// parseCreateTableStmt parses CREATE TABLE name (col1 type1, col2 type2, ...)
// and table-level CONSTRAINT / FOREIGN KEY clauses.
func (p *Parser) parseCreateTableStmt() error {
	p.advance() // consume CREATE
	if err := p.expect(lexer.KindTable); err != nil {
		return err
	}
	return p.parseCreateTableAfterTable(false)
}

// parseCreateGraphTableStmt parses CREATE GRAPH TABLE. Graph tables use the
// same relational column/constraint grammar as CREATE TABLE, but execution
// attaches the existing graph layer to the collection so record inserts
// create graph vertices through the normal collection machinery.
func (p *Parser) parseCreateGraphTableStmt() error {
	p.advance() // consume CREATE; current token is GRAPH
	if p.curr.Kind != lexer.KindIdentifier || !bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("graph")) {
		return fmt.Errorf("expected GRAPH after CREATE")
	}
	p.advance()
	if err := p.expect(lexer.KindTable); err != nil {
		return err
	}
	return p.parseCreateTableAfterTable(true)
}

func (p *Parser) parseCreateTableAfterTable(graph bool) error {

	stmt := CreateTableStmt{Graph: graph}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.TableStart = p.curr.Start
		stmt.TableEnd = p.curr.End
		p.advance()
	} else {
		return fmt.Errorf("expected table name after CREATE TABLE")
	}

	if err := p.expect(lexer.KindLeftParen); err != nil {
		return err
	}

	seenConstraintNames := make(map[string]bool)

	for p.curr.Kind != lexer.KindRightParen && p.curr.Kind != lexer.KindEOF {
		// ── Table-level constraints ──────────────────────────────
		if p.curr.Kind == lexer.KindConstraint {
			// Consume the optional constraint name once, then dispatch on
			// the actual constraint kind. This keeps table-level PRIMARY KEY
			// and FOREIGN KEY parsing unambiguous.
			p.advance()
			if !isColumnToken(p.curr.Kind) {
				return fmt.Errorf("expected constraint name after CONSTRAINT")
			}
			nameStart, nameEnd := p.curr.Start, p.curr.End
			name := string(p.src[nameStart:nameEnd])
			if seenConstraintNames[name] {
				return fmt.Errorf("duplicate constraint name %q", name)
			}
			seenConstraintNames[name] = true
			p.advance()

			switch p.curr.Kind {
			case lexer.KindPrimary:
				if stmt.PrimaryKey != nil {
					return fmt.Errorf("duplicate PRIMARY KEY constraint")
				}
				pk, err := p.parseTableLevelPrimaryKey()
				if err != nil {
					return err
				}
				pk.NameStart, pk.NameEnd = nameStart, nameEnd
				stmt.PrimaryKey = &pk
			case lexer.KindForeign:
				fk, err := p.parseTableLevelFKNamed(nameStart, nameEnd)
				if err != nil {
					return err
				}
				stmt.ForeignKeys = append(stmt.ForeignKeys, fk)
			case lexer.KindCheck:
				chk, err := p.parseCheckConstraint()
				if err != nil {
					return err
				}
				chk.NameStart = nameStart
				chk.NameEnd = nameEnd
				stmt.CheckConstraints = append(stmt.CheckConstraints, chk)
			default:
				return fmt.Errorf("unsupported table constraint %q", name)
			}
			if p.curr.Kind == lexer.KindComma {
				p.advance()
			}
			continue
		}

		if p.curr.Kind == lexer.KindPrimary {
			if stmt.PrimaryKey != nil {
				return fmt.Errorf("duplicate PRIMARY KEY constraint")
			}
			pk, err := p.parseTableLevelPrimaryKey()
			if err != nil {
				return err
			}
			stmt.PrimaryKey = &pk
			if p.curr.Kind == lexer.KindComma {
				p.advance()
			}
			continue
		}

		if p.curr.Kind == lexer.KindForeign {
			fk, err := p.parseTableLevelFK()
			if err != nil {
				return err
			}
			if fk.NameStart != 0 {
				name := string(p.src[fk.NameStart:fk.NameEnd])
				if seenConstraintNames[name] {
					return fmt.Errorf("duplicate constraint name %q", name)
				}
				seenConstraintNames[name] = true
			}
			stmt.ForeignKeys = append(stmt.ForeignKeys, fk)
			if p.curr.Kind == lexer.KindComma {
				p.advance()
			}
			continue
		}

		// Table-level CHECK without named constraint
		if p.curr.Kind == lexer.KindCheck {
			chk, err := p.parseCheckConstraint()
			if err != nil {
				return err
			}
			stmt.CheckConstraints = append(stmt.CheckConstraints, chk)
			if p.curr.Kind == lexer.KindComma {
				p.advance()
			}
			continue
		}

		// ── Column definition ──────────────────────────────────
		if !isColumnToken(p.curr.Kind) {
			return fmt.Errorf("expected column name, CONSTRAINT, or FOREIGN KEY, got %v", p.curr)
		}
		col := ColumnDef{}
		col.NameStart = p.curr.Start
		col.NameEnd = p.curr.End
		p.advance()

		if p.curr.Kind == lexer.KindIdentifier || p.curr.Kind == lexer.KindTimestamp {
			col.TypeStart = p.curr.Start
			col.TypeEnd = p.curr.End
			typeNameStart := p.curr.Start
			typeNameEnd := p.curr.End
			p.advance()
			if isTypeTimestamp(p.src, typeNameStart, typeNameEnd) {
				if err := p.parseTimestampTypeSuffix(&col.TypeEnd); err != nil {
					return fmt.Errorf("parsing type for column %q: %w",
						string(p.src[col.NameStart:col.NameEnd]), err)
				}
			}

			// VECTOR requires a dimension: VECTOR(n)
			if isTypeVector(p.src, typeNameStart, typeNameEnd) && p.curr.Kind != lexer.KindLeftParen {
				return fmt.Errorf("VECTOR type requires a dimension, e.g. VECTOR(768)")
			}
			if err := p.parseTypeParam(&col.TypeParam, &col.TypeEnd); err != nil {
				return fmt.Errorf("parsing type parameter for column %q: %w",
					string(p.src[col.NameStart:col.NameEnd]), err)
			}
		}

		// Parse column constraints (NOT NULL, PK, UNIQUE, DEFAULT, CHECK,
		// and inline REFERENCES / CONSTRAINT name REFERENCES ...).
		fks, checks, err := p.parseColumnConstraintsWithFK(&col, seenConstraintNames)
		if err != nil {
			return err
		}
		identity, err := p.parseIdentitySuffix()
		if err != nil {
			return fmt.Errorf("parsing identity clause for column %q: %w",
				string(p.src[col.NameStart:col.NameEnd]), err)
		}
		col.HasIdentity = identity
		stmt.Columns = append(stmt.Columns, col)
		stmt.ForeignKeys = append(stmt.ForeignKeys, fks...)
		stmt.CheckConstraints = append(stmt.CheckConstraints, checks...)

		if p.curr.Kind == lexer.KindComma {
			p.advance()
		} else {
			break
		}
	}

	p.expect(lexer.KindRightParen)
	p.doc.CreateTableStmts = append(p.doc.CreateTableStmts, stmt)
	return nil
}

// parseCreateEdgeTypeStmt parses CREATE EDGE TYPE name. TYPE is intentionally
// accepted as an identifier so the lexer does not need a new keyword; this
// keeps existing identifier behavior unchanged.
func (p *Parser) parseCreateEdgeTypeStmt() error {
	p.advance() // consume CREATE; current token is EDGE
	if p.curr.Kind != lexer.KindEdge {
		return fmt.Errorf("expected EDGE after CREATE")
	}
	p.advance() // TYPE
	if p.curr.Kind != lexer.KindIdentifier || !bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("type")) {
		return fmt.Errorf("expected TYPE after CREATE EDGE")
	}
	p.advance() // edge type name
	if p.curr.Kind != lexer.KindIdentifier {
		return fmt.Errorf("expected edge type name after CREATE EDGE TYPE")
	}
	stmt := CreateEdgeTypeStmt{NameStart: p.curr.Start, NameEnd: p.curr.End}
	p.advance()
	if p.curr.Kind == lexer.KindIdentifier && bytes.EqualFold(p.src[p.curr.Start:p.curr.End], []byte("undirected")) {
		stmt.Undirected = true
		stmt.DirectionSpecified = true
		p.advance()
	}
	p.doc.CreateEdgeTypeStmts = append(p.doc.CreateEdgeTypeStmts, stmt)
	return nil
}

// parseFKColumnList parses (col1, col2, ...) and returns column references.
func (p *Parser) parseFKColumnList() ([]ColumnRef, error) {
	if err := p.expect(lexer.KindLeftParen); err != nil {
		return nil, fmt.Errorf("expected '(': %w", err)
	}
	if p.curr.Kind != lexer.KindIdentifier {
		return nil, fmt.Errorf("expected column name")
	}
	var cols []ColumnRef
	for {
		cols = append(cols, ColumnRef{Start: p.curr.Start, End: p.curr.End})
		p.advance()
		if p.curr.Kind != lexer.KindComma {
			break
		}
		p.advance() // consume comma
		if p.curr.Kind != lexer.KindIdentifier {
			return nil, fmt.Errorf("expected column name after comma")
		}
	}
	if err := p.expect(lexer.KindRightParen); err != nil {
		return nil, err
	}
	return cols, nil
}

// parseTableLevelFK parses a table-level foreign key constraint:
//
//	[CONSTRAINT name] FOREIGN KEY (col [, col...]) REFERENCES table(col [, col...]) [ON DELETE action]
func (p *Parser) parseTableLevelFK() (ForeignKeyConstraint, error) {
	fk := ForeignKeyConstraint{}

	// Optional CONSTRAINT name
	if p.curr.Kind == lexer.KindConstraint {
		p.advance()
		if p.curr.Kind != lexer.KindIdentifier {
			return fk, fmt.Errorf("expected constraint name after CONSTRAINT")
		}
		fk.NameStart = p.curr.Start
		fk.NameEnd = p.curr.End
		p.advance()
	}

	// FOREIGN KEY
	if p.curr.Kind != lexer.KindForeign {
		return fk, fmt.Errorf("expected FOREIGN KEY, got %v", p.curr)
	}
	p.advance()
	if err := p.expect(lexer.KindKey); err != nil {
		return fk, err
	}

	// Source column list
	var err error
	fk.SourceColumns, err = p.parseFKColumnList()
	if err != nil {
		return fk, fmt.Errorf("in FOREIGN KEY source columns: %w", err)
	}

	// REFERENCES target table (col [, col...])
	return fk, p.parseFKReferences(&fk)
}

// parseTableLevelPrimaryKey parses PRIMARY KEY (a, b, ...). The caller must
// have positioned the parser at the PRIMARY token.
func (p *Parser) parseTableLevelPrimaryKey() (PrimaryKeyConstraint, error) {
	pk := PrimaryKeyConstraint{}
	p.advance() // PRIMARY
	if err := p.expect(lexer.KindKey); err != nil {
		return pk, err
	}
	if err := p.expect(lexer.KindLeftParen); err != nil {
		return pk, fmt.Errorf("expected '(' after PRIMARY KEY: %w", err)
	}
	if p.curr.Kind != lexer.KindIdentifier {
		return pk, fmt.Errorf("expected column name in PRIMARY KEY")
	}
	for {
		if p.curr.Kind != lexer.KindIdentifier {
			return pk, fmt.Errorf("expected column name in PRIMARY KEY")
		}
		ref := ColumnRef{Start: p.curr.Start, End: p.curr.End}
		for _, existing := range pk.Columns {
			if bytes.EqualFold(p.src[existing.Start:existing.End], p.src[ref.Start:ref.End]) {
				return pk, fmt.Errorf("duplicate column %q in PRIMARY KEY", string(p.src[ref.Start:ref.End]))
			}
		}
		pk.Columns = append(pk.Columns, ref)
		p.advance()
		if p.curr.Kind != lexer.KindComma {
			break
		}
		p.advance()
		if p.curr.Kind != lexer.KindIdentifier {
			return pk, fmt.Errorf("expected column name after ',' in PRIMARY KEY")
		}
	}
	if err := p.expect(lexer.KindRightParen); err != nil {
		return pk, err
	}
	return pk, nil
}

// parseColumnConstraintsWithFK parses column-level constraint keywords.
// Returns any inline FK and CHECK constraints discovered during parsing.
func (p *Parser) parseColumnConstraintsWithFK(col *ColumnDef, seenNames map[string]bool) (fks []ForeignKeyConstraint, checks []CheckConstraint, err error) {
	for {
		switch p.curr.Kind {
		case lexer.KindConstraint:
			// Named constraint: CONSTRAINT name [REFERENCES | PRIMARY KEY | UNIQUE | CHECK | FOREIGN KEY]
			p.advance()
			if p.curr.Kind != lexer.KindIdentifier {
				return nil, nil, fmt.Errorf("expected constraint name after CONSTRAINT")
			}
			cNameStart := p.curr.Start
			cNameEnd := p.curr.End
			name := string(p.src[cNameStart:cNameEnd])
			if seenNames[name] {
				return nil, nil, fmt.Errorf("duplicate constraint name %q", name)
			}
			seenNames[name] = true
			p.advance()

			switch p.curr.Kind {
			case lexer.KindReferences:
				fk := ForeignKeyConstraint{
					NameStart:     cNameStart,
					NameEnd:       cNameEnd,
					SourceColumns: []ColumnRef{{Start: col.NameStart, End: col.NameEnd}},
				}
				if err := p.parseFKReferences(&fk); err != nil {
					return nil, nil, err
				}
				fks = append(fks, fk)

			case lexer.KindPrimary:
				p.advance()
				p.expect(lexer.KindKey)
				col.Flags |= ColFlagPrimaryKey | ColFlagNotNull

			case lexer.KindUnique:
				p.advance()
				col.Flags |= ColFlagUnique

			case lexer.KindCheck:
				chk, cerr := p.parseCheckConstraint()
				if cerr != nil {
					return nil, nil, cerr
				}
				chk.NameStart = cNameStart
				chk.NameEnd = cNameEnd
				chk.ColumnName = string(p.src[col.NameStart:col.NameEnd])
				checks = append(checks, chk)

			case lexer.KindForeign:
				fk, ferr := p.parseTableLevelFKNamed(cNameStart, cNameEnd)
				if ferr != nil {
					return nil, nil, ferr
				}
				fks = append(fks, fk)

			default:
				return nil, nil, fmt.Errorf("unexpected token %v after CONSTRAINT name", p.curr)
			}

		case lexer.KindReferences:
			// Inline REFERENCES without named constraint
			fk := ForeignKeyConstraint{
				SourceColumns: []ColumnRef{{Start: col.NameStart, End: col.NameEnd}},
			}
			if err := p.parseFKReferences(&fk); err != nil {
				return nil, nil, err
			}
			fks = append(fks, fk)

		case lexer.KindNot:
			p.advance()
			if err := p.expect(lexer.KindNull); err != nil {
				return nil, nil, err
			}
			col.Flags |= ColFlagNotNull

		case lexer.KindPrimary:
			p.advance()
			if err := p.expect(lexer.KindKey); err != nil {
				return nil, nil, err
			}
			col.Flags |= ColFlagPrimaryKey | ColFlagNotNull

		case lexer.KindUnique:
			p.advance()
			col.Flags |= ColFlagUnique

		case lexer.KindDefault:
			p.advance()
			lit, err := p.parseDefaultLiteral()
			if err != nil {
				return nil, nil, fmt.Errorf("DEFAULT for column %q: %w",
					string(p.src[col.NameStart:col.NameEnd]), err)
			}
			col.HasDefault = true
			col.DefaultExpr = lit

		case lexer.KindCheck:
			chk, cerr := p.parseCheckConstraint()
			if cerr != nil {
				return nil, nil, cerr
			}
			chk.ColumnName = string(p.src[col.NameStart:col.NameEnd])
			checks = append(checks, chk)

		default:
			return fks, checks, nil
		}
	}
}

// parseFKReferences parses the REFERENCES target(col [, col...]) [ON DELETE action] suffix
// of a FK constraint and fills the remaining fields of fk.
func (p *Parser) parseFKReferences(fk *ForeignKeyConstraint) error {
	if err := p.expect(lexer.KindReferences); err != nil {
		return err
	}
	if p.curr.Kind != lexer.KindIdentifier {
		return fmt.Errorf("expected target table name after REFERENCES")
	}
	fk.TgtTableStart = p.curr.Start
	fk.TgtTableEnd = p.curr.End
	p.advance()

	var err error
	fk.TargetColumns, err = p.parseFKColumnList()
	if err != nil {
		return fmt.Errorf("in REFERENCES target columns: %w", err)
	}

	// Optional ON DELETE / ON UPDATE action
	for p.curr.Kind == lexer.KindOn {
		p.advance()
		switch p.curr.Kind {
		case lexer.KindDelete:
			p.advance()
			if act, err := p.parseFKAction(); err != nil {
				return fmt.Errorf("ON DELETE: %w", err)
			} else {
				fk.OnDelete = act
			}
		case lexer.KindUpdate:
			p.advance()
			if act, err := p.parseFKAction(); err != nil {
				return fmt.Errorf("ON UPDATE: %w", err)
			} else {
				fk.OnUpdate = act
			}
		default:
			return fmt.Errorf("expected DELETE or UPDATE after ON, got %v", p.curr)
		}
	}

	return nil
}

// parseFKAction parses NO ACTION | CASCADE | RESTRICT | SET NULL | SET DEFAULT
// after ON DELETE/UPDATE. NO ACTION is represented by the zero-value action.
func (p *Parser) parseFKAction() (OnDeleteAction, error) {
	switch p.curr.Kind {
	case lexer.KindNo:
		p.advance()
		if p.curr.Kind != lexer.KindAction {
			return OnDeleteNoAction, fmt.Errorf("expected ACTION after ON DELETE/UPDATE NO")
		}
		p.advance()
		return OnDeleteNoAction, nil
	case lexer.KindCascade:
		p.advance()
		return OnDeleteCascade, nil
	case lexer.KindRestrict:
		p.advance()
		return OnDeleteRestrict, nil
	case lexer.KindSet:
		p.advance()
		if p.curr.Kind == lexer.KindNull {
			p.advance()
			return OnDeleteSetNull, nil
		}
		if p.curr.Kind == lexer.KindDefault {
			p.advance()
			return OnDeleteSetDefault, nil
		}
		return OnDeleteNoAction, fmt.Errorf("expected NULL or DEFAULT after ON DELETE/UPDATE SET")
	default:
		return OnDeleteNoAction, fmt.Errorf("expected NO ACTION, CASCADE, RESTRICT, SET NULL, or SET DEFAULT after ON DELETE/UPDATE, got %v", p.curr)
	}
}

// parseTableLevelFKNamed handles CONSTRAINT name FOREIGN KEY (col) REFERENCES ...
// The name has already been consumed by the caller.
func (p *Parser) parseTableLevelFKNamed(nameStart, nameEnd uint32) (ForeignKeyConstraint, error) {
	fk := ForeignKeyConstraint{
		NameStart: nameStart,
		NameEnd:   nameEnd,
	}

	if p.curr.Kind != lexer.KindForeign {
		return fk, fmt.Errorf("expected FOREIGN KEY after constraint name, got %v", p.curr)
	}
	p.advance()
	if err := p.expect(lexer.KindKey); err != nil {
		return fk, err
	}

	var err error
	fk.SourceColumns, err = p.parseFKColumnList()
	if err != nil {
		return fk, fmt.Errorf("in FOREIGN KEY source columns: %w", err)
	}

	return fk, p.parseFKReferences(&fk)
}

// eqFold returns true if the byte slice b is equal to s under ASCII
// case-insensitive comparison. Zero-allocation — no string conversion.
func eqFold(b []byte, s string) bool {
	if len(b) != len(s) {
		return false
	}
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		d := s[i]
		if d >= 'A' && d <= 'Z' {
			d += 32
		}
		if c != d {
			return false
		}
	}
	return true
}

// skipParenthesized consumes a parenthesized expression for CHECK (...).
func (p *Parser) skipParenthesized() {
	if p.curr.Kind == lexer.KindLeftParen {
		depth := 1
		p.advance()
		for depth > 0 && p.curr.Kind != lexer.KindEOF {
			if p.curr.Kind == lexer.KindLeftParen {
				depth++
			} else if p.curr.Kind == lexer.KindRightParen {
				depth--
			}
			p.advance()
		}
	}
}

// parseDefaultLiteral parses a literal value after DEFAULT.
// Accepts: string, number, boolean (TRUE/FALSE), and NULL.
// Rejects arbitrary expressions, function calls, and identifiers.
func (p *Parser) parseDefaultLiteral() (NodeRef, error) {
	switch p.curr.Kind {
	case lexer.KindString, lexer.KindEscapeString:
		ref := NodeRef{Kind: NodeKindString, ID: int32(len(p.doc.Strings))}
		sl := StringLiteral{Start: p.curr.Start, End: p.curr.End, Escape: p.curr.Kind == lexer.KindEscapeString}
		p.doc.Strings = append(p.doc.Strings, sl)
		p.advance()
		return ref, nil
	case lexer.KindNumber:
		ref := NodeRef{Kind: NodeKindNumber, ID: int32(len(p.doc.Numbers))}
		num := Number{Start: p.curr.Start, End: p.curr.End}
		p.doc.Numbers = append(p.doc.Numbers, num)
		p.advance()
		return ref, nil
	case lexer.KindPlus, lexer.KindDash:
		signStart := p.curr.Start
		p.advance()
		if p.curr.Kind != lexer.KindNumber {
			return NodeRef{}, fmt.Errorf("DEFAULT expression expected number after sign")
		}
		ref := NodeRef{Kind: NodeKindNumber, ID: int32(len(p.doc.Numbers))}
		p.doc.Numbers = append(p.doc.Numbers, Number{Start: signStart, End: p.curr.End})
		p.advance()
		return ref, nil
	case lexer.KindNull:
		ref := NodeRef{Kind: NodeKindIdentifier, ID: int32(len(p.doc.Identifiers))}
		id := Identifier{Start: p.curr.Start, End: p.curr.End, ResolvedKind: ResolvedKindLiteral}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		p.advance()
		return ref, nil
	case lexer.KindIdentifier:
		// Accept only TRUE and FALSE as boolean literals.
		// Case-insensitive byte comparison — zero alloc.
		if eqFold(p.src[p.curr.Start:p.curr.End], "TRUE") || eqFold(p.src[p.curr.Start:p.curr.End], "FALSE") {
			ref := NodeRef{Kind: NodeKindIdentifier, ID: int32(len(p.doc.Identifiers))}
			id := Identifier{Start: p.curr.Start, End: p.curr.End, ResolvedKind: ResolvedKindLiteral}
			p.doc.Identifiers = append(p.doc.Identifiers, id)
			p.advance()
			return ref, nil
		}
		return NodeRef{}, fmt.Errorf("DEFAULT expression must be a literal (string, number, boolean, or NULL)")
	default:
		return NodeRef{}, fmt.Errorf("DEFAULT expression must be a literal (string, number, boolean, or NULL), got %v", p.curr)
	}
}

// parseCheckConstraint parses CHECK ( expression ) and returns a CheckConstraint
// with source offsets capturing the expression text. Caller sets NameStart/NameEnd
// and ColumnName as appropriate.
func (p *Parser) parseCheckConstraint() (CheckConstraint, error) {
	chk := CheckConstraint{}
	p.advance() // consume CHECK
	if err := p.expect(lexer.KindLeftParen); err != nil {
		return chk, fmt.Errorf("expected '(' after CHECK")
	}
	// Capture the expression inside the parentheses using balanced-paren tracking.
	chk.ExprStart = p.curr.Start
	depth := 1
	for depth > 0 && p.curr.Kind != lexer.KindEOF {
		if p.curr.Kind == lexer.KindLeftParen {
			depth++
		} else if p.curr.Kind == lexer.KindRightParen {
			depth--
			if depth == 0 {
				chk.ExprEnd = p.curr.Start // exclude the closing ')'
				p.advance()
				return chk, nil
			}
		}
		p.advance()
	}
	return chk, fmt.Errorf("unterminated CHECK constraint: missing ')'")
}

// parseDropTableStmt parses DROP TABLE [IF EXISTS] name.
func (p *Parser) parseDropTableStmt() error {
	p.advance() // consume DROP
	p.expect(lexer.KindTable)

	stmt := DropTableStmt{}
	// IF EXISTS
	if p.curr.Kind == lexer.KindIf {
		p.advance()
		p.expect(lexer.KindExists)
		stmt.IfExists = true
	}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.TableStart = p.curr.Start
		stmt.TableEnd = p.curr.End
		p.advance()
	} else {
		return fmt.Errorf("expected table name after DROP TABLE")
	}
	p.doc.DropTableStmts = append(p.doc.DropTableStmts, stmt)
	return nil
}

// parseDropIndexStmt parses DROP INDEX [IF EXISTS] name.
func (p *Parser) parseDropIndexStmt() error {
	p.advance() // consume DROP
	p.expect(lexer.KindIndex)

	stmt := DropIndexStmt{}
	if p.curr.Kind == lexer.KindIf {
		p.advance()
		p.expect(lexer.KindExists)
		stmt.IfExists = true
	}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.IndexStart = p.curr.Start
		stmt.IndexEnd = p.curr.End
		p.advance()
	} else {
		return fmt.Errorf("expected index name after DROP INDEX")
	}
	p.doc.DropIndexStmts = append(p.doc.DropIndexStmts, stmt)
	return nil
}

// parseCreateIndexStmt parses CREATE [UNIQUE] INDEX name ON table (col).
func (p *Parser) parseCreateIndexStmt() error {
	p.advance() // consume CREATE

	stmt := CreateIndexStmt{}
	// Optional UNIQUE
	if p.curr.Kind == lexer.KindUnique {
		stmt.Unique = true
		p.advance()
	}
	p.expect(lexer.KindIndex)
	// PostgreSQL clients and ORMs commonly emit CREATE INDEX IF NOT EXISTS.
	// The catalog/executor already treats an existing identical index as
	// idempotent; retain the clause in the AST while preserving the existing
	// index execution path.
	if p.curr.Kind == lexer.KindIf {
		p.advance()
		p.expect(lexer.KindNot)
		p.expect(lexer.KindExists)
		stmt.IfNotExists = true
	}

	if p.curr.Kind == lexer.KindIdentifier {
		stmt.IndexStart = p.curr.Start
		stmt.IndexEnd = p.curr.End
		p.advance()
	}
	p.expect(lexer.KindOn)
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.TableStart = p.curr.Start
		stmt.TableEnd = p.curr.End
		p.advance()
	}
	p.expect(lexer.KindLeftParen)
	if !isColumnToken(p.curr.Kind) {
		return fmt.Errorf("expected column name in index")
	}
	for {
		if !isColumnToken(p.curr.Kind) {
			return fmt.Errorf("expected column name in index")
		}
		ref := ColumnRef{Start: p.curr.Start, End: p.curr.End}
		stmt.Columns = append(stmt.Columns, ref)
		if len(stmt.Columns) == 1 {
			stmt.ColStart, stmt.ColEnd = ref.Start, ref.End
		}
		p.advance()
		// A JSON path expression index is represented as one column followed
		// by #> or #>> and a text-array path literal.  Keep this deliberately
		// narrow: one JSON expression per index, with no arbitrary functions.
		if len(stmt.Columns) == 1 && (p.curr.Kind == lexer.KindJSONPath || p.curr.Kind == lexer.KindJSONPathText) {
			stmt.JSONPathOperator = uint8(p.curr.Kind)
			p.advance()
			if p.curr.Kind != lexer.KindString && p.curr.Kind != lexer.KindEscapeString {
				return fmt.Errorf("expected JSON path string literal in index")
			}
			stmt.JSONPathStart = p.curr.Start
			stmt.JSONPathEnd = p.curr.End
			p.advance()
		}
		if p.curr.Kind != lexer.KindComma {
			break
		}
		p.advance()
	}
	p.expect(lexer.KindRightParen)
	p.doc.CreateIndexStmts = append(p.doc.CreateIndexStmts, stmt)
	return nil
}

// parseAlterTableStmt parses ALTER TABLE name ADD [COLUMN] col type [constraints]
// and the metadata-only DROP [COLUMN] col form used by migration tools.
func (p *Parser) parseAlterTableStmt() error {
	p.advance() // consume ALTER
	p.expect(lexer.KindTable)

	stmt := AlterTableStmt{}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.TableStart = p.curr.Start
		stmt.TableEnd = p.curr.End
		p.advance()
	} else {
		return fmt.Errorf("expected table name after ALTER TABLE")
	}

	// DROP [COLUMN] name. COLUMN is lexed as an identifier, so recognize it by
	// spelling before reading the actual column name.
	if p.curr.Kind == lexer.KindDrop {
		p.advance()
		if p.curr.Kind == lexer.KindIdentifier && strings.EqualFold(string(p.src[p.curr.Start:p.curr.End]), "COLUMN") {
			p.advance()
		}
		if p.curr.Kind != lexer.KindIdentifier {
			return fmt.Errorf("expected column name after ALTER TABLE DROP COLUMN")
		}
		stmt.DropColumn = true
		stmt.DropColumnStart = p.curr.Start
		stmt.DropColumnEnd = p.curr.End
		p.advance()
		p.doc.AlterTableStmts = append(p.doc.AlterTableStmts, stmt)
		return nil
	}

	// ADD [COLUMN]
	if p.curr.Kind == lexer.KindAdd {
		p.advance()
	}
	// Optional COLUMN keyword. COLUMN is lexed as an identifier, so it must be
	// recognized by spelling before reading the actual column name.
	if p.curr.Kind == lexer.KindIdentifier && strings.EqualFold(string(p.src[p.curr.Start:p.curr.End]), "COLUMN") {
		p.advance()
	}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.AddColumn.NameStart = p.curr.Start
		stmt.AddColumn.NameEnd = p.curr.End
		p.advance()
	}
	if p.curr.Kind == lexer.KindIdentifier || p.curr.Kind == lexer.KindTimestamp {
		stmt.AddColumn.TypeStart = p.curr.Start
		stmt.AddColumn.TypeEnd = p.curr.End
		typeNameStart := p.curr.Start
		typeNameEnd := p.curr.End
		p.advance()
		if isTypeTimestamp(p.src, typeNameStart, typeNameEnd) {
			if err := p.parseTimestampTypeSuffix(&stmt.AddColumn.TypeEnd); err != nil {
				return fmt.Errorf("parsing type for column %q: %w",
					string(p.src[stmt.AddColumn.NameStart:stmt.AddColumn.NameEnd]), err)
			}
		}

		// VECTOR requires a dimension: VECTOR(n)
		if isTypeVector(p.src, typeNameStart, typeNameEnd) && p.curr.Kind != lexer.KindLeftParen {
			return fmt.Errorf("VECTOR type requires a dimension, e.g. VECTOR(768)")
		}
		if err := p.parseTypeParam(&stmt.AddColumn.TypeParam, &stmt.AddColumn.TypeEnd); err != nil {
			return fmt.Errorf("parsing type parameter for column %q: %w",
				string(p.src[stmt.AddColumn.NameStart:stmt.AddColumn.NameEnd]), err)
		}
	}

	// Parse constraints: NOT NULL, PRIMARY KEY, UNIQUE
	_, _, err := p.parseColumnConstraintsWithFK(&stmt.AddColumn, make(map[string]bool))
	if err != nil {
		return err
	}

	p.doc.AlterTableStmts = append(p.doc.AlterTableStmts, stmt)
	return nil
}

func (p *Parser) parseJoinClause() (JoinClause, error) {
	jc := JoinClause{Type: JoinInner} // default to INNER
	optional := false
	if p.curr.Kind == lexer.KindOptional {
		optional = true
		p.advance()
		// Accept both Cypher's OPTIONAL MATCH and the explicit
		// OPTIONAL JOIN MATCH spelling.
		if p.curr.Kind == lexer.KindJoin {
			p.advance()
		}
		if p.curr.Kind != lexer.KindMatch {
			return JoinClause{}, fmt.Errorf("OPTIONAL requires MATCH")
		}
		jc.Type = JoinLeft
	}

	// Parse optional join type qualifier: LEFT, RIGHT, FULL, INNER, CROSS, OUTER
	switch p.curr.Kind {
	case lexer.KindLeft:
		jc.Type = JoinLeft
		p.advance()
		// Optional OUTER: LEFT OUTER JOIN
		if p.curr.Kind == lexer.KindOuter {
			p.advance()
		}
	case lexer.KindRight:
		jc.Type = JoinRight
		p.advance()
		if p.curr.Kind == lexer.KindOuter {
			p.advance()
		}
	case lexer.KindFull:
		jc.Type = JoinFull
		p.advance()
		if p.curr.Kind == lexer.KindOuter {
			p.advance()
		}
	case lexer.KindInner:
		jc.Type = JoinInner
		p.advance()
	case lexer.KindCross:
		jc.Type = JoinCross
		p.advance()
	case lexer.KindOuter:
		// Bare OUTER without LEFT/RIGHT/FULL — treat as INNER
		p.advance()
	}

	// Now expect JOIN keyword
	if p.curr.Kind == lexer.KindJoin {
		p.advance() // consume JOIN
	}

	// Graph join: JOIN MATCH (a)-[e]->(b)
	// No table name — the join is defined entirely by the match path.
	if p.curr.Kind == lexer.KindMatch {
		p.advance()
		matchRef, err := p.parseMatchPath()
		if err != nil {
			return JoinClause{}, err
		}
		jc.MatchPath = matchRef
		jc.Optional = optional
		// Optional ON clause after the match path: JOIN MATCH (...) ON s.id = x.id
		if p.curr.Kind == lexer.KindOn {
			p.advance()
			onRef, err := p.parseExpr(0)
			if err != nil {
				return JoinClause{}, err
			}
			jc.OnExpr = onRef
		}
		return jc, nil
	}

	// Derived table JOIN: JOIN (SELECT ...) [AS] alias ON ...
	if p.curr.Kind == lexer.KindLeftParen && p.next.Kind == lexer.KindSelect {
		ref, err := p.parseTableExpr()
		if err != nil {
			return JoinClause{}, err
		}
		t := p.doc.TableExprs[ref.ID]
		jc.Derived = ref
		jc.TableStart, jc.TableEnd = t.Start, t.End
		jc.Alias, jc.AliasEnd = t.Alias, t.AliasEnd
		if p.curr.Kind == lexer.KindOn {
			p.advance()
			onRef, err := p.parseExpr(0)
			if err != nil {
				return JoinClause{}, err
			}
			jc.OnExpr = onRef
		}
		return jc, nil
	}

	// Set-returning table function JOIN. The function is represented by the
	// same FunctionExpr arena used for FROM table functions.
	if p.curr.Kind == lexer.KindIdentifier && p.next.Kind == lexer.KindLeftParen {
		fn, err := p.parseFunctionExpr()
		if err != nil {
			return JoinClause{}, fmt.Errorf("table function JOIN: %w", err)
		}
		jc.Function = fn
		jc.IsFunction = true
		if p.curr.Kind == lexer.KindAs {
			p.advance()
		}
		if p.curr.Kind != lexer.KindIdentifier {
			return JoinClause{}, fmt.Errorf("table function JOIN requires an alias")
		}
		jc.Alias, jc.AliasEnd = p.curr.Start, p.curr.End
		p.advance()
		if p.curr.Kind == lexer.KindOn {
			p.advance()
			onRef, err := p.parseExpr(0)
			if err != nil {
				return JoinClause{}, err
			}
			jc.OnExpr = onRef
		}
		return jc, nil
	}

	if p.curr.Kind == lexer.KindIdentifier {
		jc.TableStart = p.curr.Start
		jc.TableEnd = p.curr.End
		p.advance()
	}
	// Optional alias
	if p.curr.Kind == lexer.KindAs {
		p.advance()
		if p.curr.Kind != lexer.KindIdentifier {
			return JoinClause{}, errors.New("expected joined table alias after AS")
		}
		jc.Alias = p.curr.Start
		jc.AliasEnd = p.curr.End
		p.advance()
	} else if p.curr.Kind == lexer.KindIdentifier {
		jc.Alias = p.curr.Start
		jc.AliasEnd = p.curr.End
		p.advance()
	}
	// ON clause
	if p.curr.Kind == lexer.KindOn {
		p.advance()
		onRef, err := p.parseExpr(0)
		if err != nil {
			return JoinClause{}, err
		}
		jc.OnExpr = onRef
	}
	return jc, nil
}

// isTypeVector returns true if the source bytes from start to end spell
// "VECTOR" case-insensitively.
func isTypeVector(src []byte, start, end uint32) bool {
	if end-start != 6 {
		return false
	}
	b := src[start : start+6]
	return (b[0] == 'V' || b[0] == 'v') &&
		(b[1] == 'E' || b[1] == 'e') &&
		(b[2] == 'C' || b[2] == 'c') &&
		(b[3] == 'T' || b[3] == 't') &&
		(b[4] == 'O' || b[4] == 'o') &&
		(b[5] == 'R' || b[5] == 'r')
}

// parseDimension reads a positive integer from src[start:end] and returns it
// as uint32. Rejects zero, negative, non-numeric, and values that overflow
// the AST's uint32 representation. The parser deliberately does not impose a
// model-specific embedding ceiling; execution may apply platform/allocation
// limits when creating the vector index.
func parseDimension(src []byte, start, end uint32) (uint32, error) {
	if start >= end {
		return 0, fmt.Errorf("dimension must be a positive integer")
	}
	var v uint32
	for i := start; i < end; i++ {
		c := src[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("dimension must be a positive integer, got unexpected character %q", c)
		}
		digit := uint32(c - '0')
		if v > (^uint32(0)-digit)/10 {
			return 0, fmt.Errorf("dimension %s exceeds uint32 maximum", string(src[start:end]))
		}
		v = v*10 + digit
	}
	if v == 0 {
		return 0, fmt.Errorf("dimension must be positive, got 0")
	}
	return v, nil
}

func isTypeTimestamp(src []byte, start, end uint32) bool {
	return bytes.EqualFold(src[start:end], []byte("TIMESTAMP"))
}

// parseIdentitySuffix consumes the PostgreSQL identity clause emitted by
// Django for AutoField primary keys. Identity generation is represented by
// the existing primary-key metadata; the storage layer does not need a
// separate sequence object for this compatibility path.
func (p *Parser) parseIdentitySuffix() (bool, error) {
	if p.curr.Kind != lexer.KindIdentifier || !strings.EqualFold(string(p.src[p.curr.Start:p.curr.End]), "GENERATED") {
		return false, nil
	}
	p.advance()
	byDefault := false
	if p.curr.Kind == lexer.KindIdentifier && strings.EqualFold(string(p.src[p.curr.Start:p.curr.End]), "ALWAYS") {
		p.advance()
	} else if p.curr.Kind == lexer.KindBy {
		byDefault = true
		p.advance()
	} else {
		return false, fmt.Errorf("expected ALWAYS or BY after GENERATED")
	}
	if byDefault {
		if p.curr.Kind != lexer.KindDefault {
			return false, fmt.Errorf("expected DEFAULT after GENERATED BY")
		}
		p.advance()
	}
	if p.curr.Kind != lexer.KindAs {
		return false, fmt.Errorf("expected AS after GENERATED identity mode")
	}
	p.advance()
	if p.curr.Kind != lexer.KindIdentifier || !strings.EqualFold(string(p.src[p.curr.Start:p.curr.End]), "IDENTITY") {
		return false, fmt.Errorf("expected IDENTITY after GENERATED ... AS")
	}
	p.advance()
	return true, nil
}

// parseTimestampTypeSuffix consumes PostgreSQL's multi-word timestamp
// qualifier. Django's PostgreSQL backend emits `timestamp with time zone` for
// DateTimeField columns.
func (p *Parser) parseTimestampTypeSuffix(typeEnd *uint32) error {
	without := p.curr.Kind == lexer.KindIdentifier && strings.EqualFold(string(p.src[p.curr.Start:p.curr.End]), "WITHOUT")
	if p.curr.Kind != lexer.KindWith && !without {
		return nil
	}
	p.advance()
	if p.curr.Kind != lexer.KindIdentifier || !strings.EqualFold(string(p.src[p.curr.Start:p.curr.End]), "TIME") {
		return fmt.Errorf("expected TIME after TIMESTAMP qualifier")
	}
	p.advance()
	if p.curr.Kind != lexer.KindIdentifier || !strings.EqualFold(string(p.src[p.curr.Start:p.curr.End]), "ZONE") {
		return fmt.Errorf("expected ZONE after TIMESTAMP qualifier")
	}
	*typeEnd = p.curr.End
	p.advance()
	return nil
}

// parseTypeParam attempts to consume a parenthesized integer parameter after a
// type token. If the current token is '(', it consumes ( <number> ) and stores
// the parsed value in *dim. TypeEnd is extended to cover the closing ')' so the
// raw type string includes the parameter. Returns an error only on malformed
// input — if there is no '(' the function is a no-op.
func (p *Parser) parseTypeParam(dim *uint32, typeEnd *uint32) error {
	if p.curr.Kind != lexer.KindLeftParen {
		return nil
	}
	p.advance() // consume '('
	if p.curr.Kind != lexer.KindNumber {
		return fmt.Errorf("expected numeric dimension in VECTOR(n), got kind %d", p.curr.Kind)
	}
	d, err := parseDimension(p.src, p.curr.Start, p.curr.End)
	if err != nil {
		return err
	}
	*dim = d
	p.advance() // consume number
	if p.curr.Kind != lexer.KindRightParen {
		return fmt.Errorf("expected ')' after VECTOR(%d)", d)
	}
	*typeEnd = p.curr.End // extend TypeEnd to cover the ')'
	p.advance()           // consume ')'
	return nil
}
