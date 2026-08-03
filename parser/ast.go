package parser

// NodeKind identifies which SoA slice a NodeRef points to.
type NodeKind uint8

const (
	NodeKindUnknown NodeKind = iota
	NodeKindSelectStmt
	NodeKindProjection
	NodeKindTableExpr
	NodeKindGraphTable
	NodeKindMatchPath
	NodeKindVertex
	NodeKindEdge
	NodeKindBinaryExpr
	NodeKindVectorFunc
	NodeKindBetweenExpr
	NodeKindInExpr
	NodeKindUnaryExpr
	NodeKindIdentifier
	NodeKindNumber
	NodeKindString
	NodeKindAggregateExpr
	NodeKindSubqueryExpr
	NodeKindCreateTableStmt
	NodeKindDropTableStmt
	NodeKindCreateIndexStmt
)

const (
	ResolvedKindUnknown uint8 = 0
	ResolvedKindTable   uint8 = 1
	ResolvedKindColumn  uint8 = 2
	ResolvedKindVector  uint8 = 3
	ResolvedKindGraph   uint8 = 4
)

// NodeRef allows the AST to reference children without pointers.
type NodeRef struct {
	Kind NodeKind
	ID   int32
}

// QueryDoc holds the entire AST in contiguous memory slices (Structure of Arrays).
// The parser populates this doc with zero allocations per node by using preallocated slices.
type QueryDoc struct {
	SelectStmts []SelectStmt
	Projections []Projection
	TableExprs  []TableExpr
	GraphTables []GraphTable
	MatchPaths  []MatchPath
	Vertexes    []Vertex
	Edges       []Edge
	BinaryExprs []BinaryExpr
	VectorFuncs []VectorFunc
	BetweenExprs []BetweenExpr
	InExprs     []InExpr
	UnaryExprs  []UnaryExpr
	Identifiers []Identifier
	Numbers     []Number
	Strings     []StringLiteral
	Nodes       []NodeRef // The root node(s) of the query

	// CRUD statements
	InsertStmts []InsertStmt
	UpdateStmts []UpdateStmt
	DeleteStmts []DeleteStmt

	// Aggregate / Subquery / DDL
	AggregateExprs  []AggregateExpr
	SubqueryExprs   []SubqueryExpr
	CreateTableStmts  []CreateTableStmt
	DropTableStmts    []DropTableStmt
	DropIndexStmts    []DropIndexStmt
	CreateIndexStmts  []CreateIndexStmt
	AlterTableStmts   []AlterTableStmt
}

// JoinType enumerates SQL join variants.
type JoinType uint8

const (
	JoinInner JoinType = iota
	JoinLeft
	JoinRight
	JoinFull
	JoinCross
)

// JoinClause represents a JOIN ... ON clause, or a JOIN MATCH graph join.
type JoinClause struct {
	TableStart uint32
	TableEnd   uint32
	Alias      uint32 // offset to alias, 0 if none
	AliasEnd   uint32
	OnExpr     NodeRef
	MatchPath  NodeRef // graph join: JOIN MATCH (a)-[e]->(b); zero value if not a graph join
	Type       JoinType // INNER (default), LEFT, RIGHT, FULL, CROSS
}

// SelectStmt represents a SELECT query.
type SelectStmt struct {
	ID               int32
	ProjectionsStart int32   // Index into QueryDoc.Projections
	ProjectionsCount int32
	FromTable        NodeRef // Points to TableExpr or GraphTable
	Joins            []JoinClause
	WhereExpr        NodeRef // Points to BinaryExpr or VectorFunc, etc.
	GroupBy          []NodeRef // GROUP BY column references
	HavingExpr       NodeRef   // HAVING clause expression
	OrderBy          NodeRef   // Points to the expression being ordered
	Limit            int32     // Index to Number node, or -1
	IsDesc           bool
}

// InsertStmt represents INSERT INTO ... VALUES ...
type InsertStmt struct {
	TableStart uint32
	TableEnd   uint32
	Columns    []NodeRef // column identifiers
	Values     []NodeRef // value expressions (number/string literals)
}

// UpdateStmt represents UPDATE ... SET ... WHERE ...
type UpdateStmt struct {
	TableStart uint32
	TableEnd   uint32
	SetColumns []NodeRef // column identifiers
	SetValues  []NodeRef // value expressions
	WhereExpr  NodeRef
}

// DeleteStmt represents DELETE FROM ... WHERE ...
type DeleteStmt struct {
	TableStart uint32
	TableEnd   uint32
	WhereExpr  NodeRef
}

// Projection is a single item in a SELECT list.
type Projection struct {
	ID       int32
	Expr     NodeRef
	Alias    uint32 // Offset into source string, 0 if none
	AliasEnd uint32
}

// TableExpr represents a standard relational table.
type TableExpr struct {
	ID       int32
	Start    uint32
	End      uint32
	Alias    uint32 // Offset to alias (e.g., 's' in FROM services s), 0 if none
	AliasEnd uint32
	TableOID uint32 // Resolved at bind time
}

// GraphTable represents the GRAPH_TABLE(...) clause.
type GraphTable struct {
	ID         int32
	TableStart uint32 // Offset of the base graph table name
	TableEnd   uint32
	TableOID   uint32 // Resolved at bind time
	MatchPath  NodeRef
}

// MatchPath represents the entire chain of (a)-[e]->(b)
type MatchPath struct {
	ID            int32
	ElementsStart int32 // Index into a flat list of MatchElements, but for simplicity we can interleave Vertex/Edge manually or use references
	// For SoA, we can store a slice of NodeRefs in the doc just for paths
	PathNodesStart int32 // Index into a general NodeRef slice for the path sequence
	PathNodesCount int32
}

// Vertex represents a node in a MATCH path (e.g. `(a)` or `(a:Label)`).
type Vertex struct {
	ID       int32
	Alias    uint32 // Offset to alias (e.g., 'a' in (a))
	AliasEnd uint32
	LabelStart uint32 // Offset to label identifier (e.g., 'Label' in (a:Label)), 0 if no label
	LabelEnd   uint32
}

// Edge represents -[e]- or -> graph connections, with optional type annotation.
type Edge struct {
	ID        int32
	Alias     uint32 // Offset to alias (e.g., 'e' in -[e]->)
	AliasEnd  uint32
	TypeStart uint32 // Offset to edge type identifier (e.g., 'KNOWS' in [e:KNOWS]), 0 if no type
	TypeEnd   uint32
	Direction int8   // -1: left, 0: undirected, 1: right

	// Path quantifier: controls hop count for multi-hop traversals.
	// No quantifier (default): QuantMin=0, QuantMax=0 → exactly 1 hop.
	// ->+ : QuantMin=1, QuantMax=QuantUnbounded → 1 or more hops.
	// ->* : QuantMin=0, QuantMax=QuantUnbounded → 0 or more hops.
	// {min,max}: QuantMin=min, QuantMax=max → between min and max hops.
	// *min..max: QuantMin=min, QuantMax=max (asterisk-range form).
	QuantMin uint16
	QuantMax uint16
}

// QuantUnbounded is the sentinel value for an unbounded max hop count.
const QuantUnbounded uint16 = 0xFFFF

// BinaryExpr represents an operator like a = b or a > b.
type BinaryExpr struct {
	ID       int32
	Left     NodeRef
	Right    NodeRef
	Operator uint8 // Token Kind of the operator (e.g., KindEquals, KindGreaterThan, KindAnd, KindOr)
}

type BetweenExpr struct {
	ID    int32
	Expr  NodeRef
	Lower NodeRef
	Upper NodeRef
	Not   bool
}

type InExpr struct {
	ID        int32
	Expr      NodeRef
	ListStart int32
	ListCount int32
	Not       bool
}

type UnaryExpr struct {
	ID       int32
	Expr     NodeRef
	Operator uint8
}

// VectorFunc represents SIMILARITY() or VECTOR_DISTANCE().
type VectorFunc struct {
	ID         int32
	IsMaxSim   bool   // true for SIMILARITY, false for VECTOR_DISTANCE
	VectorA    NodeRef // Left operand (e.g. Identifier)
	VectorB    NodeRef // Right operand (e.g. Array or Identifier)
}

// Identifier represents a column, table, or alias.
type Identifier struct {
	ID           int32
	Start        uint32
	End          uint32
	QualStart    uint32 // Offset to qualifier alias (e.g., 's' in s.owner_id), 0 if none
	QualEnd      uint32
	TableOID     uint32 // Resolved at bind time
	ColumnOID    uint32 // Resolved at bind time
	ResolvedKind uint8  // ResolvedKindTable, ResolvedKindColumn, etc.
}

// Number represents a numeric literal.
type Number struct {
	ID    int32
	Start uint32
	End   uint32
}

// StringLiteral represents a string literal.
type StringLiteral struct {
	ID    int32
	Start uint32
	End   uint32
}

// AggregateFunc enumerates supported aggregate functions.
type AggregateFunc uint8

const (
	AggCount  AggregateFunc = 0
	AggSum    AggregateFunc = 1
	AggAvg    AggregateFunc = 2
	AggMin    AggregateFunc = 3
	AggMax    AggregateFunc = 4
)

// AggregateExpr represents COUNT(*), COUNT(col), SUM(col), AVG(col), MIN(col), MAX(col).
type AggregateExpr struct {
	ID       int32
	Func     AggregateFunc
	Distinct bool
	Expr     NodeRef // column reference, or zero-value NodeRef for COUNT(*)
}

// SubqueryExpr represents a parenthesized SELECT subquery.
type SubqueryExpr struct {
	ID   int32
	Stmt NodeRef // points to a nested SelectStmt
}

// CreateTableStmt represents CREATE TABLE name (col1 type1, col2 type2, ...).
type CreateTableStmt struct {
	TableStart uint32
	TableEnd   uint32
	Columns    []ColumnDef
}

// ColumnFlags bitmask for column constraints.
const (
	ColFlagNotNull uint16 = 1 << iota
	ColFlagPrimaryKey
	ColFlagUnique
)

// ColumnDef is a single column definition in CREATE TABLE.
type ColumnDef struct {
	NameStart uint32
	NameEnd   uint32
	TypeStart uint32
	TypeEnd   uint32
	Flags     uint16 // ColFlagNotNull, ColFlagPrimaryKey, ColFlagUnique
}

// DropTableStmt represents DROP TABLE [IF EXISTS] name.
type DropTableStmt struct {
	TableStart uint32
	TableEnd   uint32
	IfExists   bool
}

// DropIndexStmt represents DROP INDEX [IF EXISTS] name.
type DropIndexStmt struct {
	IndexStart uint32
	IndexEnd   uint32
	IfExists   bool
}

// CreateIndexStmt represents CREATE INDEX name ON table (col).
type CreateIndexStmt struct {
	IndexStart uint32
	IndexEnd   uint32
	TableStart uint32
	TableEnd   uint32
	ColStart   uint32
	ColEnd     uint32
	Unique     bool
}

// AlterTableStmt represents ALTER TABLE name ADD [COLUMN] col type [constraints].
type AlterTableStmt struct {
	TableStart uint32
	TableEnd   uint32
	AddColumn  ColumnDef // ADD COLUMN clause
}

// Reset clears the slices to zero length while retaining capacity.
// This allows the QueryDoc to be reused across queries with zero allocations.
func (d *QueryDoc) Reset() {
	d.SelectStmts = d.SelectStmts[:0]
	d.Projections = d.Projections[:0]
	d.TableExprs = d.TableExprs[:0]
	d.GraphTables = d.GraphTables[:0]
	d.MatchPaths = d.MatchPaths[:0]
	d.Vertexes = d.Vertexes[:0]
	d.Edges = d.Edges[:0]
	d.BinaryExprs = d.BinaryExprs[:0]
	d.VectorFuncs = d.VectorFuncs[:0]
	d.BetweenExprs = d.BetweenExprs[:0]
	d.InExprs = d.InExprs[:0]
	d.UnaryExprs = d.UnaryExprs[:0]
	d.Identifiers = d.Identifiers[:0]
	d.Numbers = d.Numbers[:0]
	d.Strings = d.Strings[:0]
	d.Nodes = d.Nodes[:0]
	d.InsertStmts = d.InsertStmts[:0]
	d.UpdateStmts = d.UpdateStmts[:0]
	d.DeleteStmts = d.DeleteStmts[:0]
	d.AggregateExprs = d.AggregateExprs[:0]
	d.SubqueryExprs = d.SubqueryExprs[:0]
	d.CreateTableStmts = d.CreateTableStmts[:0]
	d.DropTableStmts = d.DropTableStmts[:0]
	d.DropIndexStmts = d.DropIndexStmts[:0]
	d.CreateIndexStmts = d.CreateIndexStmts[:0]
	d.AlterTableStmts = d.AlterTableStmts[:0]
	for i := range d.SelectStmts {
		d.SelectStmts[i].Joins = nil
		d.SelectStmts[i].GroupBy = nil
	}
}
