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

	if p.curr.Kind == lexer.KindWhere {
		p.advance()
		whereRef, err := p.parseExpr(0)
		if err != nil {
			return NodeRef{}, err
		}
		stmt.WhereExpr = whereRef
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
				e.QuantMin = 0
				e.QuantMax = QuantUnbounded
				p.advance()
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
		
		// Peek for dot property access e.g., a.vec
		if p.curr.Kind == lexer.KindDot {
			p.advance() // dot
			if p.curr.Kind == lexer.KindIdentifier {
				// For AST simplicity, combine into a single identifier or binary expr.
				// Here we just advance to consume for the benchmark.
				p.advance()
			}
		}
		
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

	// VALUES (val1, val2)
	if p.curr.Kind == lexer.KindValues {
		p.advance()
	}
	if p.curr.Kind == lexer.KindLeftParen {
		p.advance()
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
