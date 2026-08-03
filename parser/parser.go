package parser

import (
	"errors"
	"fmt"

	"github.com/xDarkicex/lexer"
)

// Parser holds the state for converting tokens into an AST.
type Parser struct {
	scanner *lexer.Scanner
	doc     *QueryDoc
	src     []byte // stored for zero-alloc number parsing from token offsets
	curr    lexer.Token
	next    lexer.Token
}

// Parse parses a SQL/PGQ byte stream into the provided QueryDoc.
// It resets the doc before parsing to guarantee zero allocations.
func Parse(src []byte, doc *QueryDoc) error {
	doc.Reset()
	p := &Parser{
		scanner: lexer.New(src),
		doc:     doc,
		src:     src,
	}
	// prime the pump
	p.advance()
	p.advance()

	for p.curr.Kind != lexer.KindEOF {
		switch p.curr.Kind {
		case lexer.KindSelect:
			stmtRef, err := p.parseSelectStmt()
			if err != nil {
				return err
			}
			doc.Nodes = append(doc.Nodes, stmtRef)
		case lexer.KindInsert:
			return p.parseInsertStmt()
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
		default:
			// If we hit unexpected tokens at root, we break or error.
			// For this subset parser, just break.
			return fmt.Errorf("unexpected token at root: %v", p.curr.Kind)
		}
	}
	return nil
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

func (p *Parser) expect(kind lexer.Kind) error {
	if p.curr.Kind != kind {
		return fmt.Errorf("expected %v, got %v", kind, p.curr.Kind)
	}
	p.advance()
	return nil
}

func (p *Parser) parseSelectStmt() (NodeRef, error) {
	p.advance() // consume SELECT

	stmt := SelectStmt{
		ID:               int32(len(p.doc.SelectStmts)),
		ProjectionsStart: int32(len(p.doc.Projections)),
		Limit:            -1,
	}
	
	// Parse projections
	for p.curr.Kind != lexer.KindEOF && p.curr.Kind != lexer.KindFrom {
		proj, err := p.parseProjection()
		if err != nil {
			return NodeRef{}, err
		}
		p.doc.Projections = append(p.doc.Projections, proj)
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
	}

	// Parse JOIN clauses
	for p.curr.Kind == lexer.KindJoin ||
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
			p.curr.Kind != lexer.KindLimit {
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

	if p.curr.Kind == lexer.KindOrder {
		p.advance()
		if err := p.expect(lexer.KindBy); err != nil {
			return NodeRef{}, err
		}
		orderRef, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		stmt.OrderBy = orderRef
		if p.curr.Kind == lexer.KindDesc {
			stmt.IsDesc = true
			p.advance()
		}
	}

	if p.curr.Kind == lexer.KindLimit {
		p.advance()
		if p.curr.Kind == lexer.KindNumber {
			num := Number{
				ID:    int32(len(p.doc.Numbers)),
				Start: p.curr.Start,
				End:   p.curr.End,
			}
			p.doc.Numbers = append(p.doc.Numbers, num)
			stmt.Limit = num.ID
			p.advance()
		}
	}

	p.doc.SelectStmts = append(p.doc.SelectStmts, stmt)
	return NodeRef{Kind: NodeKindSelectStmt, ID: stmt.ID}, nil
}

func (p *Parser) parseProjection() (Projection, error) {
	expr, err := p.parseExpr(0)
	if err != nil {
		return Projection{}, err
	}
	proj := Projection{
		ID:   int32(len(p.doc.Projections)),
		Expr: expr,
	}
	// Optional column alias: SELECT expr AS alias
	if p.curr.Kind == lexer.KindAs {
		p.advance()
		if p.curr.Kind == lexer.KindIdentifier {
			proj.Alias = p.curr.Start
			proj.AliasEnd = p.curr.End
			p.advance()
		}
	}
	return proj, nil
}

func (p *Parser) parseTableExpr() (NodeRef, error) {
	if p.curr.Kind == lexer.KindGraphTable {
		p.advance()
		return p.parseGraphTable()
	}
	
	if p.curr.Kind == lexer.KindIdentifier {
		t := TableExpr{
			ID:    int32(len(p.doc.TableExprs)),
			Start: p.curr.Start,
			End:   p.curr.End,
		}
		p.advance()
		// Optional alias: FROM services s
		if p.curr.Kind == lexer.KindIdentifier {
			t.Alias = p.curr.Start
			t.AliasEnd = p.curr.End
			p.advance()
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

func (p *Parser) parseMatchPath() (NodeRef, error) {
	mp := MatchPath{
		ID:             int32(len(p.doc.MatchPaths)),
		PathNodesStart: int32(len(p.doc.Nodes)),
	}
	
	for {
		if p.curr.Kind == lexer.KindLeftParen {
			// Vertex
			p.advance()
			v := Vertex{
				ID: int32(len(p.doc.Vertexes)),
			}
			if p.curr.Kind == lexer.KindIdentifier {
				v.Alias = p.curr.Start
				v.AliasEnd = p.curr.End
				p.advance()
			}
			if p.curr.Kind == lexer.KindColon {
				p.advance()
				if p.curr.Kind == lexer.KindIdentifier {
					v.LabelStart = p.curr.Start
					v.LabelEnd = p.curr.End
					p.advance()
				} else {
					return NodeRef{}, fmt.Errorf("expected vertex label after colon")
				}
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
				if err := p.expect(lexer.KindDash); err != nil {
					return NodeRef{}, err
				}
			} else {
				p.advance() // consume dash
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

// Simple Pratt parser stub for expressions.
func (p *Parser) parseExpr(precedence int) (NodeRef, error) {
	// NUD (Null Denotation)
	var left NodeRef
	switch p.curr.Kind {
	case lexer.KindIdentifier:
		id := Identifier{
			ID:    int32(len(p.doc.Identifiers)),
			Start: p.curr.Start,
			End:   p.curr.End,
		}
		left = NodeRef{Kind: NodeKindIdentifier, ID: id.ID}
		p.doc.Identifiers = append(p.doc.Identifiers, id)
		p.advance()
		
		// Peek for dot property access e.g., a.vec — capture the qualifier so
		// the binder can resolve s.owner_id against the FROM alias s.
		if p.curr.Kind == lexer.KindDot {
			p.advance() // dot
			if p.curr.Kind == lexer.KindIdentifier {
				id.QualStart = id.Start
				id.QualEnd = id.End
				id.Start = p.curr.Start
				id.End = p.curr.End
				p.advance()
			}
		}
		
	case lexer.KindCount, lexer.KindSum, lexer.KindAvg, lexer.KindMin, lexer.KindMax:
		left = p.parseAggregate()

	case lexer.KindSimilarity, lexer.KindVectorDistance:
		isSim := p.curr.Kind == lexer.KindSimilarity
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
		
	case lexer.KindString:
		sl := StringLiteral{
			ID:    int32(len(p.doc.Strings)),
			Start: p.curr.Start,
			End:   p.curr.End,
		}
		left = NodeRef{Kind: NodeKindString, ID: sl.ID}
		p.doc.Strings = append(p.doc.Strings, sl)
		p.advance()
	case lexer.KindNumber:
		num := Number{
			ID:    int32(len(p.doc.Numbers)),
			Start: p.curr.Start,
			End:   p.curr.End,
		}
		left = NodeRef{Kind: NodeKindNumber, ID: num.ID}
		p.doc.Numbers = append(p.doc.Numbers, num)
		p.advance()
	case lexer.KindLeftBracket:
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
			if err := p.expect(lexer.KindLeftParen); err != nil {
				return NodeRef{}, err
			}
			inNode := InExpr{
				ID:        int32(len(p.doc.InExprs)),
				Expr:      left,
				ListStart: int32(len(p.doc.Nodes)),
				Not:       isNot,
			}
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
			if err := p.expect(lexer.KindRightParen); err != nil {
				return NodeRef{}, err
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

// parseAggregate parses COUNT(*), COUNT(col), SUM(col), AVG(col), MIN(col), MAX(col).
func (p *Parser) parseAggregate() NodeRef {
	funcKind := p.curr.Kind
	p.advance() // consume func name

	ae := AggregateExpr{
		ID:   int32(len(p.doc.AggregateExprs)),
		Func: aggregateFuncFromKind(funcKind),
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
		p.doc.AggregateExprs = append(p.doc.AggregateExprs, ae)
		return NodeRef{Kind: NodeKindAggregateExpr, ID: ae.ID}
	}

	// DISTINCT modifier
	if p.curr.Kind == lexer.KindIdentifier {
		// We'll parse the argument as an expression
		expr, err := p.parseExpr(0)
		if err == nil {
			ae.Expr = expr
		}
	}

	p.expect(lexer.KindRightParen)
	p.doc.AggregateExprs = append(p.doc.AggregateExprs, ae)
	return NodeRef{Kind: NodeKindAggregateExpr, ID: ae.ID}
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
		lexer.KindL2Dist, lexer.KindIPDist, lexer.KindCosineDist:
		return 4
	}
	return 0
}

func (p *Parser) parseInsertStmt() error {
	p.advance() // consume INSERT
	p.expect(lexer.KindInto)

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
			if p.curr.Kind == lexer.KindIdentifier {
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

	// VALUES (val1, val2), (val3, val4), ...
	if p.curr.Kind == lexer.KindValues {
		p.advance()
	}
	// Parse one or more tuple groups: (a, b, c), (d, e, f), ...
	for p.curr.Kind == lexer.KindLeftParen {
		p.advance() // consume '('
		for p.curr.Kind != lexer.KindRightParen && p.curr.Kind != lexer.KindEOF {
			switch p.curr.Kind {
			case lexer.KindString:
				sl := StringLiteral{ID: int32(len(p.doc.Strings)), Start: p.curr.Start, End: p.curr.End}
				p.doc.Strings = append(p.doc.Strings, sl)
				stmt.Values = append(stmt.Values, NodeRef{Kind: NodeKindString, ID: sl.ID})
				p.advance()
			case lexer.KindNumber:
				num := Number{ID: int32(len(p.doc.Numbers)), Start: p.curr.Start, End: p.curr.End}
				p.doc.Numbers = append(p.doc.Numbers, num)
				stmt.Values = append(stmt.Values, NodeRef{Kind: NodeKindNumber, ID: num.ID})
				p.advance()
			default:
				p.advance()
			}
			if p.curr.Kind == lexer.KindComma {
				p.advance()
			}
		}
		p.expect(lexer.KindRightParen)
		// Skip any comma separating this tuple from the next
		if p.curr.Kind == lexer.KindComma {
			p.advance()
		}
	}

	p.doc.InsertStmts = append(p.doc.InsertStmts, stmt)
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
		for p.curr.Kind != lexer.KindWhere && p.curr.Kind != lexer.KindEOF {
			if p.curr.Kind == lexer.KindIdentifier {
				id := Identifier{ID: int32(len(p.doc.Identifiers)), Start: p.curr.Start, End: p.curr.End}
				p.doc.Identifiers = append(p.doc.Identifiers, id)
				stmt.SetColumns = append(stmt.SetColumns, NodeRef{Kind: NodeKindIdentifier, ID: id.ID})
				p.advance()
			}
			if p.curr.Kind == lexer.KindEquals {
				p.advance()
			}
			if p.curr.Kind == lexer.KindString {
				sl := StringLiteral{ID: int32(len(p.doc.Strings)), Start: p.curr.Start, End: p.curr.End}
				p.doc.Strings = append(p.doc.Strings, sl)
				stmt.SetValues = append(stmt.SetValues, NodeRef{Kind: NodeKindString, ID: sl.ID})
				p.advance()
			} else if p.curr.Kind == lexer.KindNumber {
				num := Number{ID: int32(len(p.doc.Numbers)), Start: p.curr.Start, End: p.curr.End}
				p.doc.Numbers = append(p.doc.Numbers, num)
				stmt.SetValues = append(stmt.SetValues, NodeRef{Kind: NodeKindNumber, ID: num.ID})
				p.advance()
			} else {
				p.advance()
			}
			if p.curr.Kind == lexer.KindComma {
				p.advance()
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

	p.doc.DeleteStmts = append(p.doc.DeleteStmts, stmt)
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
func (p *Parser) parseCreateTableStmt() error {
	p.advance() // consume CREATE
	p.expect(lexer.KindTable)

	stmt := CreateTableStmt{}
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

	for p.curr.Kind != lexer.KindRightParen && p.curr.Kind != lexer.KindEOF {
		col := ColumnDef{}
		if p.curr.Kind == lexer.KindIdentifier {
			col.NameStart = p.curr.Start
			col.NameEnd = p.curr.End
			p.advance()
		} else {
			break
		}
		if p.curr.Kind == lexer.KindIdentifier {
			col.TypeStart = p.curr.Start
			col.TypeEnd = p.curr.End
			p.advance()
		}
		// Parse constraints: NOT NULL, PRIMARY KEY, UNIQUE, DEFAULT, CHECK, REFERENCES
		parseColumnConstraints(p, &col)
		stmt.Columns = append(stmt.Columns, col)
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
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.ColStart = p.curr.Start
		stmt.ColEnd = p.curr.End
		p.advance()
	}
	p.expect(lexer.KindRightParen)
	p.doc.CreateIndexStmts = append(p.doc.CreateIndexStmts, stmt)
	return nil
}

// parseAlterTableStmt parses ALTER TABLE name ADD [COLUMN] col type [constraints].
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

	// ADD [COLUMN]
	if p.curr.Kind == lexer.KindAdd {
		p.advance()
	}
	// Optional COLUMN keyword
	if p.curr.Kind == lexer.KindIdentifier {
		// Could be "COLUMN" or the actual column name
		// Simple heuristic: if it looks like "COLUMN", skip it
		colName := string(p.src[p.curr.Start:p.curr.End])
		_ = colName
		// We handle the case where COLUMN is an identifier — just advance and read next
	}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.AddColumn.NameStart = p.curr.Start
		stmt.AddColumn.NameEnd = p.curr.End
		p.advance()
	}
	if p.curr.Kind == lexer.KindIdentifier {
		stmt.AddColumn.TypeStart = p.curr.Start
		stmt.AddColumn.TypeEnd = p.curr.End
		p.advance()
	}

	// Parse constraints: NOT NULL, PRIMARY KEY, UNIQUE
	parseColumnConstraints(p, &stmt.AddColumn)

	p.doc.AlterTableStmts = append(p.doc.AlterTableStmts, stmt)
	return nil
}

// parseColumnConstraints parses column-level constraint keywords and sets flags on the ColumnDef.
// Handles: NOT NULL, PRIMARY KEY, UNIQUE, DEFAULT value
func parseColumnConstraints(p *Parser, col *ColumnDef) {
	for {
		switch p.curr.Kind {
		case lexer.KindNot:
			p.advance()
			if p.curr.Kind == lexer.KindNull {
				p.advance()
				col.Flags |= ColFlagNotNull
			}
		case lexer.KindPrimary:
			p.advance()
			if p.curr.Kind == lexer.KindKey {
				p.advance()
				col.Flags |= ColFlagPrimaryKey | ColFlagNotNull
			}
		case lexer.KindUnique:
			p.advance()
			col.Flags |= ColFlagUnique
		case lexer.KindDefault:
			p.advance()
			// Skip the default value expression
			p.parseExpr(0)
		case lexer.KindCheck:
			p.advance()
			// Skip CHECK (...) — consume parenthesized expression
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
		case lexer.KindReferences:
			p.advance()
			// Skip REFERENCES table(col) — consume identifiers and parenthesized
			if p.curr.Kind == lexer.KindIdentifier {
				p.advance()
			}
			if p.curr.Kind == lexer.KindLeftParen {
				p.advance()
				if p.curr.Kind == lexer.KindIdentifier {
					p.advance()
				}
				p.expect(lexer.KindRightParen)
			}
		case lexer.KindConstraint:
			p.advance()
			// Skip named constraint — e.g. CONSTRAINT pk PRIMARY KEY
			if p.curr.Kind == lexer.KindIdentifier {
				p.advance()
			}
			// Fall through to parse the actual constraint type
			continue
		default:
			return
		}
	}
}

func (p *Parser) parseJoinClause() (JoinClause, error) {
	jc := JoinClause{Type: JoinInner} // default to INNER

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

	if p.curr.Kind == lexer.KindIdentifier {
		jc.TableStart = p.curr.Start
		jc.TableEnd = p.curr.End
		p.advance()
	}
	// Optional alias
	if p.curr.Kind == lexer.KindIdentifier {
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
