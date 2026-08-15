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
	NodeKindGraphMetric
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
	NodeKindInsertGraphEdgeStmt
	NodeKindComputeLeidenStmt
	NodeKindCaseExpr
	NodeKindFunctionExpr
	NodeKindCastExpr
	NodeKindPrepareStmt
	NodeKindExecuteStmt
	NodeKindSessionSettingStmt
)

const (
	ResolvedKindUnknown uint8 = 0
	ResolvedKindTable   uint8 = 1
	ResolvedKindColumn  uint8 = 2
	ResolvedKindVector  uint8 = 3
	ResolvedKindGraph   uint8 = 4
	ResolvedKindLiteral uint8 = 5 // TRUE, FALSE, NULL — skip binder resolution
	// ResolvedKindExcluded marks the EXCLUDED pseudo-row used by
	// INSERT ... ON CONFLICT DO UPDATE expressions.
	ResolvedKindExcluded uint8 = 6
)

// NodeRef allows the AST to reference children without pointers.
type NodeRef struct {
	Kind NodeKind
	ID   int32
}

// QueryDoc holds the entire AST in contiguous memory slices (Structure of Arrays).
// The parser populates this doc with zero allocations per node by using preallocated slices.
type QueryDoc struct {
	// Transaction statements are represented independently of row-producing
	// statements so database adapters can bind them to a session transaction.
	TransactionStmts []TransactionStmt
	SelectStmts      []SelectStmt
	Projections      []Projection
	TableExprs       []TableExpr
	GraphTables      []GraphTable
	MatchPaths       []MatchPath
	Vertexes         []Vertex
	Edges            []Edge
	BinaryExprs      []BinaryExpr
	VectorFuncs      []VectorFunc
	GraphMetrics     []GraphMetricExpr
	BetweenExprs     []BetweenExpr
	InExprs          []InExpr
	UnaryExprs       []UnaryExpr
	Identifiers      []Identifier
	Numbers          []Number
	Strings          []StringLiteral
	CaseExprs        []CaseExpr
	CaseWhens        []CaseWhen
	FunctionExprs    []FunctionExpr
	FunctionArgs     []NodeRef // argument arena for nested function expressions
	WindowSpecs      []WindowSpec
	WindowOrders     []WindowOrder
	NamedWindows     []NamedWindow
	CastExprs        []CastExpr
	Nodes            []NodeRef // The root node(s) of the query

	// CRUD statements
	InsertStmts          []InsertStmt
	InsertGraphEdgeStmts []InsertGraphEdgeStmt
	UpdateStmts          []UpdateStmt
	DeleteStmts          []DeleteStmt

	// Aggregate / Subquery / DDL
	AggregateExprs      []AggregateExpr
	SubqueryExprs       []SubqueryExpr
	CreateTableStmts    []CreateTableStmt
	CreateEdgeTypeStmts []CreateEdgeTypeStmt
	DropTableStmts      []DropTableStmt
	DropIndexStmts      []DropIndexStmt
	CreateIndexStmts    []CreateIndexStmt
	AlterTableStmts     []AlterTableStmt

	// ComputeLeidenStmts holds COMPUTE LEIDEN statements.
	ComputeLeidenStmts  []ComputeLeidenStmt
	LeidenOptions       []LeidenOption
	CTEs                []CTE
	PrepareStmts        []PrepareStmt
	ExecuteStmts        []ExecuteStmt
	ExecuteArgs         []NodeRef
	SessionSettingStmts []SessionSettingStmt

	// Reusable per-SELECT backing arenas. Each SELECT gets an independent
	// arena so nested SELECT parsing cannot invalidate or merge a parent
	// statement's Joins, GroupBy, or OrderTerms slices.
	joinArenas       [][]JoinClause
	groupByArenas    [][]NodeRef
	orderTermsArenas [][]OrderTerm
}

func (d *QueryDoc) reusableJoinArena(selectID int) []JoinClause {
	for len(d.joinArenas) <= selectID {
		d.joinArenas = append(d.joinArenas, nil)
	}
	arena := d.joinArenas[selectID][:0]
	d.joinArenas[selectID] = arena
	return arena
}

func (d *QueryDoc) reusableGroupByArena(selectID int) []NodeRef {
	for len(d.groupByArenas) <= selectID {
		d.groupByArenas = append(d.groupByArenas, nil)
	}
	arena := d.groupByArenas[selectID][:0]
	d.groupByArenas[selectID] = arena
	return arena
}

func (d *QueryDoc) reusableOrderTermsArena(selectID int) []OrderTerm {
	for len(d.orderTermsArenas) <= selectID {
		d.orderTermsArenas = append(d.orderTermsArenas, nil)
	}
	arena := d.orderTermsArenas[selectID][:0]
	d.orderTermsArenas[selectID] = arena
	return arena
}

type TransactionKind uint8

const (
	TransactionBegin TransactionKind = iota
	TransactionBeginEpoch
	TransactionCommit
	TransactionRollback
	TransactionSavepoint
	TransactionRollbackToSavepoint
	TransactionReleaseSavepoint
)

type TransactionStmt struct {
	Kind          TransactionKind
	SavepointName string
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
	// Derived references a parenthesized SELECT used as the JOIN relation.
	// TableStart/TableEnd retain the source span and Alias names the virtual
	// relation; no catalog table is required for this side.
	Derived NodeRef
	// Function is a set-returning table function used on the right side of a
	// JOIN, such as CROSS JOIN jsonb_array_elements(d.payload) AS elem.
	Function   NodeRef
	IsFunction bool
	Type       JoinType // INNER (default), LEFT, RIGHT, FULL, CROSS
}

// SelectStmt represents a SELECT query.
type SelectStmt struct {
	ID               int32
	SourceStart      uint32
	SourceEnd        uint32
	ProjectionsStart int32 // Index into QueryDoc.Projections
	ProjectionsCount int32
	Distinct         bool
	FromTable        NodeRef // Points to TableExpr or GraphTable
	Joins            []JoinClause
	WhereExpr        NodeRef   // Points to BinaryExpr or VectorFunc, etc.
	GroupBy          []NodeRef // GROUP BY column references
	HavingExpr       NodeRef   // HAVING clause expression
	OrderBy          NodeRef   // Points to the expression being ordered
	// OrderTerms contains the complete ORDER BY list. OrderBy/IsDesc retain
	// the first term for older planner paths that only support one key.
	OrderTerms []OrderTerm
	Limit      int32   // Index to Number node, or -1
	Offset     int32   // Index to Number node, or -1
	LimitExpr  NodeRef // Typed numeric parameter expression, when LIMIT is parameterized
	OffsetExpr NodeRef // Typed numeric parameter expression, when OFFSET is parameterized
	IsDesc     bool
	UnionNext  NodeRef
	UnionAll   bool
	UnionStart uint32
	SetOp      SetOperation
	SetOpAll   bool
	// CTE fields — populated when WITH precedes SELECT.
	CTEsStart       int32 // Index into QueryDoc.CTEs
	CTEsCount       int32
	WindowDefsStart int32
	WindowDefsCount int32
}

// OrderTerm is one expression and direction in a SELECT ORDER BY list.
// NULLS ordering remains outside this compact representation for now.
type OrderTerm struct {
	Expr   NodeRef
	IsDesc bool
}

// SetOperation identifies the SQL set operator connecting a SELECT to its
// right-hand branch. SetOpNone means the statement is not a set expression.
type SetOperation uint8

const (
	SetOpNone SetOperation = iota
	SetOpUnion
	SetOpIntersect
	SetOpExcept
)

// CTE represents a single WITH ... AS (...) common table expression.
// Body references a SELECT or COMPUTE LEIDEN statement in the document arena.
type CTE struct {
	ID        int32
	NameStart uint32
	NameEnd   uint32
	Body      NodeRef
	Recursive bool
}

// InsertStmt represents INSERT INTO ... VALUES ...
type InsertStmt struct {
	TableStart  uint32
	TableEnd    uint32
	Columns     []NodeRef // column identifiers
	Values      []NodeRef // value expressions (number/string literals)
	Select      NodeRef   // INSERT ... SELECT source
	HasSelect   bool
	SelectStart uint32
	SelectEnd   uint32
	// ON CONFLICT is represented as part of the INSERT statement so lowering
	// and execution cannot silently ignore an unsupported tail.
	ConflictColumns []NodeRef // empty means conflict target omitted
	// ConflictConstraintStart/End identify ON CONFLICT ON CONSTRAINT name.
	// A zero-length span means the conflict target is column-based or omitted.
	ConflictConstraintStart uint32
	ConflictConstraintEnd   uint32
	ConflictAction          uint8 // 0 none, 1 DO NOTHING, 2 DO UPDATE
	ConflictSet             []InsertConflictAssignment
	ConflictWhere           NodeRef
	HasConflictWhere        bool
	Returning               []NodeRef // RETURNING column list
	ReturningStar           bool      // RETURNING *
}

type PrepareStmt struct {
	ID         int32
	NameStart  uint32
	NameEnd    uint32
	QueryStart uint32
	QueryEnd   uint32
}

type ExecuteStmt struct {
	ID        int32
	NameStart uint32
	NameEnd   uint32
	ArgsStart int32
	ArgsCount int32
}

// SessionSetting identifies the supported per-session controls. These are
// deliberately a closed set: accepting a setting that has no execution
// semantics is worse than rejecting it.
type SessionSetting uint8

const (
	SessionSettingStatementTimeout SessionSetting = iota
	SessionSettingMaxRecursionDepth
	SessionSettingEnableSeqScan
)

// SessionSettingStmt represents SET/RESET without converting its value to a
// Go string. Name and value offsets/refs preserve the source for the binder.
type SessionSettingStmt struct {
	ID        int32
	Kind      SessionSetting
	NameStart uint32
	NameEnd   uint32
	Value     NodeRef
	Reset     bool
	Local     bool
}

// InsertConflictAssignment represents one ON CONFLICT DO UPDATE SET item.
// Value is used for ordinary literals/parameters. When ExcludedColumn is
// non-zero, the value is EXCLUDED.<column> and must be taken from the proposed
// INSERT row during execution.
type InsertConflictAssignment struct {
	Column         NodeRef
	Value          NodeRef
	ExcludedColumn NodeRef
}

// InsertGraphEdgeStmt represents INSERT INTO GRAPH_EDGES VALUES
// (src, edge_kind, tgt [, properties]). Properties is a JSON object literal.
type InsertGraphEdgeStmt struct {
	SrcStart        uint32 // source record ID offset in source bytes
	SrcEnd          uint32
	EdgeKindStart   uint32 // edge kind name offset in source bytes
	EdgeKindEnd     uint32
	TgtStart        uint32 // target record ID offset in source bytes
	TgtEnd          uint32
	PropertiesStart uint32
	PropertiesEnd   uint32
	HasProperties   bool
	// Expression refs preserve bound parameters for GRAPH_EDGES DML. The
	// legacy offsets above remain populated for literal strings and keep the
	// existing zero-copy literal path compatible.
	SrcExpr        NodeRef
	EdgeKindExpr   NodeRef
	TgtExpr        NodeRef
	PropertiesExpr NodeRef
}

// LeidenOptionKind identifies supported COMPUTE LEIDEN OPTIONS.
type LeidenOptionKind uint8

const (
	LeidenOptionResolution LeidenOptionKind = iota
	LeidenOptionIterations
	LeidenOptionMaxLevels
	LeidenOptionMaxLocalMovingPasses
	LeidenOptionMinHops
	LeidenOptionMaxHops
	LeidenOptionMaxVertices
	LeidenOptionMaxEdges
	LeidenOptionEdgeKind
	LeidenOptionDirection
)

// LeidenOption is a single name = value entry in OPTIONS (...).
// Offset fields point into the source byte slice. Values remain as
// NodeRef so numeric literals stay as NodeKindNumber — exact source
// literal parsing is deferred to the lowering phase.
type LeidenOption struct {
	Kind      LeidenOptionKind
	NameStart uint32
	NameEnd   uint32
	Value     NodeRef
}

// ComputeLeidenStmt represents the full COMPUTE LEIDEN statement.
// MatchPath references an existing NodeKindMatchPath in QueryDoc.MatchPaths.
// Options are stored as a flat slice with OptionsStart/OptionsCount
// indexing into QueryDoc.LeidenOptions.
type ComputeLeidenStmt struct {
	ID           int32
	MatchPath    NodeRef
	OptionsStart int32
	OptionsCount int32
}

// UpdateStmt represents UPDATE ... SET ... WHERE ...
type UpdateStmt struct {
	TableStart    uint32
	TableEnd      uint32
	SetColumns    []NodeRef // column identifiers
	SetValues     []NodeRef // value expressions
	WhereExpr     NodeRef
	Returning     []NodeRef // RETURNING column list
	ReturningStar bool      // RETURNING *
}

// DeleteStmt represents DELETE FROM ... WHERE ...
type DeleteStmt struct {
	TableStart    uint32
	TableEnd      uint32
	WhereExpr     NodeRef
	Returning     []NodeRef // RETURNING column list
	ReturningStar bool      // RETURNING *
}

// Projection is a single item in a SELECT list.
type Projection struct {
	ID       int32
	Expr     NodeRef
	Alias    uint32 // Offset into source string, 0 if none
	AliasEnd uint32
	Star     bool // SELECT * — all columns
}

// TableExpr represents a standard relational table.
type TableExpr struct {
	ID       int32
	Start    uint32
	End      uint32
	Alias    uint32 // Offset to alias (e.g., 's' in FROM services s), 0 if none
	AliasEnd uint32
	TableOID uint32 // Resolved at bind time
	// Derived references a parenthesized SELECT used as a virtual relation in
	// FROM. IsDerived distinguishes it from a physical table expression.
	Derived   NodeRef
	IsDerived bool
	// Function is a set-returning SQL function used as a FROM source, for
	// example jsonb_array_elements(payload) AS elem. It remains a normal
	// FunctionExpr so argument binding and source offsets are shared with
	// scalar function calls.
	Function   NodeRef
	IsFunction bool
	// AS OF TIMESTAMP — when non-empty, the query executes at this system-time
	// snapshot. TimestampStart/End point into the source byte slice (RFC3339).
	Temporal       bool
	TimestampStart uint32
	TimestampEnd   uint32
	// VERSIONS OF table BETWEEN TIMESTAMP start AND TIMESTAMP end selects
	// retained record versions whose validity interval overlaps the range.
	// The four offsets preserve the original timestamp literals/parameters.
	TemporalRange   bool
	RangeStartStart uint32
	RangeStartEnd   uint32
	RangeEndStart   uint32
	RangeEndEnd     uint32
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
	ID         int32
	Alias      uint32 // Offset to alias (e.g., 'a' in (a))
	AliasEnd   uint32
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
	Direction int8 // -1: left, 0: undirected, 1: right

	// Path quantifier: controls hop count for multi-hop traversals.
	// No quantifier (default): QuantMin=0, QuantMax=0 → exactly 1 hop.
	// ->+ : QuantMin=1, QuantMax=QuantUnbounded → 1 or more hops.
	// ->* : QuantMin=0, QuantMax=QuantUnbounded → 0 or more hops.
	// {min,max}: QuantMin=min, QuantMax=max → between min and max hops.
	// *min..max: QuantMin=min, QuantMax=max (asterisk-range form).
	QuantMin uint16
	QuantMax uint16

	// Predicate is an optional edge-local WHERE expression, for example
	// [r:RELATES WHERE r.weight > 0.8]. The expression remains in the
	// existing expression arena so lowering can validate it without reparsing.
	Predicate NodeRef
}

// QuantUnbounded is the sentinel value for an unbounded max hop count.
const QuantUnbounded uint16 = 0xFFFF

// BinaryExpr represents an operator like a = b or a > b.
type BinaryExpr struct {
	ID       int32
	Left     NodeRef
	Right    NodeRef
	Operator uint8 // Token Kind of the operator (e.g., KindEquals, KindGreaterThan, KindAnd, KindOr)
	// NullTest is non-zero for IS NULL / IS NOT NULL predicates.
	NullTest uint8
}

const (
	NullTestNone    uint8 = 0
	NullTestIsNull  uint8 = 1
	NullTestNotNull uint8 = 2
)

type BetweenExpr struct {
	ID    int32
	Expr  NodeRef
	Lower NodeRef
	Upper NodeRef
	Not   bool
}

type InExpr struct {
	ID          int32
	Expr        NodeRef
	ListStart   int32
	ListCount   int32
	Subquery    NodeRef
	HasSubquery bool
	Not         bool
}

type UnaryExpr struct {
	ID       int32
	Expr     NodeRef
	Operator uint8
}

// VectorFunc represents SIMILARITY() or VECTOR_DISTANCE().
type VectorFunc struct {
	ID       int32
	IsMaxSim bool    // true for SIMILARITY, false for VECTOR_DISTANCE
	VectorA  NodeRef // Left operand (e.g. Identifier)
	VectorB  NodeRef // Right operand (e.g. Array or Identifier)
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

// GraphMetricExpr represents GRAPH_CENTRALITY(node) or similar graph metric functions.
type GraphMetricExpr struct {
	ID      int32
	Operand NodeRef // The graph node identifier
	Kind    uint8   // 0 = centrality, 1 = pagerank (future)
}

// Number represents a numeric literal.
type Number struct {
	ID    int32
	Start uint32
	End   uint32
}

// StringLiteral represents a string literal.
type StringLiteral struct {
	ID     int32
	Start  uint32
	End    uint32
	Escape bool // PostgreSQL E'...' escape semantics
}

type CaseWhen struct {
	Condition NodeRef
	Value     NodeRef
}

type CaseExpr struct {
	ID         int32
	WhensStart int32
	WhensCount int32
	Else       NodeRef
	HasElse    bool
}

type FunctionExpr struct {
	ID              int32
	NameStart       uint32
	NameEnd         uint32
	ArgsStart       int32
	ArgsCount       int32
	WindowID        int32
	HasWindow       bool
	WindowNameStart uint32
	WindowNameEnd   uint32
}

type WindowNullsOrder uint8

const (
	WindowNullsDefault WindowNullsOrder = iota
	WindowNullsFirst
	WindowNullsLast
)

// WindowOrder is one expression in a window ORDER BY list.
type WindowOrder struct {
	Expr       NodeRef
	IsDesc     bool
	NullsOrder WindowNullsOrder
}

type WindowFrameBoundKind uint8

const (
	WindowFrameCurrentRow WindowFrameBoundKind = iota
	WindowFrameUnboundedPreceding
	WindowFrameUnboundedFollowing
	WindowFramePreceding
	WindowFrameFollowing
)

type WindowFrameBound struct {
	Kind WindowFrameBoundKind
	Expr NodeRef // optional offset expression for PRECEDING/FOLLOWING
}

type WindowFrame struct {
	HasFrame bool
	IsRange  bool
	Start    WindowFrameBound
	End      WindowFrameBound
}

// WindowSpec represents OVER (PARTITION BY ... ORDER BY ...). Expressions
// reference the existing expression arena; no SQL text is rewritten.
type WindowSpec struct {
	ID             int32
	PartitionStart int32
	PartitionCount int32
	OrderStart     int32
	OrderCount     int32
	Frame          WindowFrame
	// OrderBy/IsDesc retain the first-order compatibility view used by older
	// callers. New code should use QueryDoc.WindowOrders via OrderStart/Count.
	OrderBy NodeRef
	IsDesc  bool
}

type NamedWindow struct {
	ID        int32
	NameStart uint32
	NameEnd   uint32
	SpecID    int32
}

type CastExpr struct {
	ID        int32
	Expr      NodeRef
	TypeStart uint32
	TypeEnd   uint32
}

// AggregateFunc enumerates supported aggregate functions.
type AggregateFunc uint8

const (
	AggCount          AggregateFunc = 0
	AggSum            AggregateFunc = 1
	AggAvg            AggregateFunc = 2
	AggMin            AggregateFunc = 3
	AggMax            AggregateFunc = 4
	AggPercentileCont AggregateFunc = 5
	AggPercentileDisc AggregateFunc = 6
	AggMode           AggregateFunc = 7
	// AggVectorAvg is the component-wise average of a vector column.
	// Keep this append-only: AggregateFunc values are persisted in physical
	// plans and consumed by protocol adapters.
	AggVectorAvg AggregateFunc = 8
)

// AggregateExpr represents ordinary aggregates and ordered-set aggregates.
// For ordered-set forms such as PERCENTILE_CONT(0.5) WITHIN GROUP
// (ORDER BY score), Expr is the direct argument and OrderExpr is the
// WITHIN GROUP ordering expression.
type AggregateExpr struct {
	ID              int32
	Func            AggregateFunc
	Distinct        bool
	Expr            NodeRef // column reference, or zero-value NodeRef for COUNT(*)
	OrderedSet      bool
	OrderExpr       NodeRef
	OrderDesc       bool
	WindowID        int32
	HasWindow       bool
	WindowNameStart uint32
	WindowNameEnd   uint32
}

// SubqueryExpr represents a parenthesized SELECT subquery.
type SubqueryExpr struct {
	ID     int32
	Stmt   NodeRef // points to a nested SelectStmt
	Exists bool    // true when parsed from EXISTS (SELECT ...)
}

// CreateTableStmt represents CREATE TABLE name (col1 type1, col2 type2, ...).
type CreateTableStmt struct {
	TableStart       uint32
	TableEnd         uint32
	Graph            bool // CREATE GRAPH TABLE; records become graph vertices.
	Columns          []ColumnDef
	ForeignKeys      []ForeignKeyConstraint
	PrimaryKey       *PrimaryKeyConstraint
	CheckConstraints []CheckConstraint
}

// CreateEdgeTypeStmt represents CREATE EDGE TYPE name. The database assigns
// and durably records the numeric graph kind during execution.
type CreateEdgeTypeStmt struct {
	NameStart          uint32
	NameEnd            uint32
	Undirected         bool
	DirectionSpecified bool
}

// CheckConstraint represents a CHECK (...) constraint, either inline on a
// column or at table level. The expression is stored as source offsets for
// lowering without reparsing SQL text.
type CheckConstraint struct {
	NameStart  uint32 // constraint name (0 if unnamed/auto)
	NameEnd    uint32
	ExprStart  uint32 // start of expression text (after '(' and any whitespace)
	ExprEnd    uint32 // end of expression text (before closing ')')
	ColumnName string // non-empty for inline column CHECK; empty for table-level
}

// ColumnFlags bitmask for column constraints.
const (
	ColFlagNotNull uint16 = 1 << iota
	ColFlagPrimaryKey
	ColFlagUnique
)

// OnDeleteAction for foreign key ON DELETE / ON UPDATE clauses.
type OnDeleteAction uint8

const (
	OnDeleteNoAction OnDeleteAction = iota
	OnDeleteCascade
	OnDeleteRestrict
	OnDeleteSetNull
	OnDeleteSetDefault
)

// PrimaryKeyConstraint represents a table-level PRIMARY KEY declaration.
// Column-level PRIMARY KEY declarations continue to use ColumnDef.Flags.
// Columns contains source offsets so lowering can resolve names without
// reparsing SQL text.
type PrimaryKeyConstraint struct {
	NameStart uint32
	NameEnd   uint32
	Columns   []ColumnRef
}

// ColumnRef is a source-offset reference to a column identifier.
type ColumnRef struct {
	Start uint32
	End   uint32
}

// ForeignKeyConstraint represents a FOREIGN KEY declaration in CREATE TABLE,
// either inline (REFERENCES on a column) or table-level.
// SourceColumns and TargetColumns carry source offsets into the original SQL
// for lowering without reparsing. Single-column FKs have one entry in each.
type ForeignKeyConstraint struct {
	NameStart     uint32 // constraint name (0 if unnamed/auto)
	NameEnd       uint32
	TgtTableStart uint32 // target table name
	TgtTableEnd   uint32
	SourceColumns []ColumnRef // source column references
	TargetColumns []ColumnRef // target column references
	OnDelete      OnDeleteAction
	OnUpdate      OnDeleteAction // reused enum: NoAction, Cascade, Restrict
}

// ColumnDef is a single column definition in CREATE TABLE.
type ColumnDef struct {
	NameStart   uint32
	NameEnd     uint32
	TypeStart   uint32
	TypeEnd     uint32
	TypeParam   uint32 // dimension for VECTOR(n); 0 for unparameterized types
	Flags       uint16 // ColFlagNotNull, ColFlagPrimaryKey, ColFlagUnique
	HasDefault  bool   // true when DEFAULT <literal> was parsed
	HasIdentity bool   // true when GENERATED ... AS IDENTITY was parsed
	DefaultExpr NodeRef
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
	IndexStart  uint32
	IndexEnd    uint32
	TableStart  uint32
	TableEnd    uint32
	ColStart    uint32
	ColEnd      uint32
	Columns     []ColumnRef
	Unique      bool
	IfNotExists bool
	// JSONPathStart/End and JSONPathOperator are populated for an expression
	// index such as `(payload#>>'{profile,active}')`.  The source offsets are
	// retained so the binder can preserve the exact path literal without
	// reparsing SQL text.
	JSONPathStart    uint32
	JSONPathEnd      uint32
	JSONPathOperator uint8
}

// AlterTableStmt represents ALTER TABLE name ADD [COLUMN] col type [constraints]
// or ALTER TABLE name DROP [COLUMN] col.
type AlterTableStmt struct {
	TableStart      uint32
	TableEnd        uint32
	AddColumn       ColumnDef // ADD COLUMN clause
	DropColumn      bool
	DropColumnStart uint32
	DropColumnEnd   uint32
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
	d.CaseExprs = d.CaseExprs[:0]
	d.CaseWhens = d.CaseWhens[:0]
	d.FunctionExprs = d.FunctionExprs[:0]
	d.FunctionArgs = d.FunctionArgs[:0]
	d.WindowSpecs = d.WindowSpecs[:0]
	d.WindowOrders = d.WindowOrders[:0]
	d.NamedWindows = d.NamedWindows[:0]
	d.CastExprs = d.CastExprs[:0]
	d.Nodes = d.Nodes[:0]
	d.InsertStmts = d.InsertStmts[:0]
	d.InsertGraphEdgeStmts = d.InsertGraphEdgeStmts[:0]
	d.UpdateStmts = d.UpdateStmts[:0]
	d.DeleteStmts = d.DeleteStmts[:0]
	d.AggregateExprs = d.AggregateExprs[:0]
	d.SubqueryExprs = d.SubqueryExprs[:0]
	d.CreateTableStmts = d.CreateTableStmts[:0]
	d.CreateEdgeTypeStmts = d.CreateEdgeTypeStmts[:0]
	d.DropTableStmts = d.DropTableStmts[:0]
	d.DropIndexStmts = d.DropIndexStmts[:0]
	d.CreateIndexStmts = d.CreateIndexStmts[:0]
	d.AlterTableStmts = d.AlterTableStmts[:0]
	d.TransactionStmts = d.TransactionStmts[:0]
	d.ComputeLeidenStmts = d.ComputeLeidenStmts[:0]
	d.LeidenOptions = d.LeidenOptions[:0]
	d.CTEs = d.CTEs[:0]
	d.PrepareStmts = d.PrepareStmts[:0]
	d.ExecuteStmts = d.ExecuteStmts[:0]
	d.ExecuteArgs = d.ExecuteArgs[:0]
	d.SessionSettingStmts = d.SessionSettingStmts[:0]
	for i := range d.SelectStmts {
		d.SelectStmts[i].Joins = nil
		d.SelectStmts[i].GroupBy = nil
	}
}
