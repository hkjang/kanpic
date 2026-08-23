// Package presentation turns a range of a workbook into a deck.
//
// The point of this package is that a spreadsheet knows things about its own
// numbers that a presentation tool cannot see: which column names the thing
// being measured, which one is the measurement, whether a column is a share of
// a whole or a change against last year, and which row is the one worth
// mentioning. Handing raw cells to a deck generator throws all of that away and
// asks it to guess.
//
// So the pipeline is: cells -> Analysis (what the range means) -> Deck (what to
// say about it) -> a provider's own format. Only the last step knows about any
// particular presentation product.
package presentation

import "time"

// SourceRef is where a deck's numbers came from. It is carried all the way to
// the provider so a deck can later be pointed back at the range that made it.
type SourceRef struct {
	WorkbookID string    `json:"workbook_id"`
	SheetID    string    `json:"sheet_id"`
	SheetName  string    `json:"sheet_name,omitempty"`
	Range      string    `json:"range"`
	Version    int64     `json:"version"`
	CapturedAt time.Time `json:"captured_at"`
}

// Column kinds. A column is classified once and the whole deck follows from it.
const (
	ColumnText    = "text"
	ColumnNumber  = "number"
	ColumnPercent = "percent"
	ColumnDate    = "date"
	ColumnEmpty   = "empty"
)

// Roles a column plays in the story rather than in the data. A percentage
// column called "목표달성률" and one called "전년대비" hold the same kind of
// number and mean opposite things when one of them is 91.
const (
	RoleDimension  = "dimension"
	RoleMeasure    = "measure"
	RoleChange     = "change"
	RoleAttainment = "attainment"
	RoleShare      = "share"
	RoleStage      = "stage"
	RoleDetail     = "detail"
)

type Column struct {
	Index  int     `json:"index"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	Role   string  `json:"role"`
	Values []Value `json:"-"`
}

// Value keeps both what the cell said and what it meant. The text is what a
// slide should print — "120억" reads better than 120 — and the number is what a
// chart has to draw.
type Value struct {
	Text   string  `json:"text"`
	Number float64 `json:"number"`
	IsNum  bool    `json:"is_number"`
	Blank  bool    `json:"blank"`
}

// Analysis is what the range turned out to be.
// Group is one category's total across rows that repeat it. Raw rows are the
// normal shape of a spreadsheet — one line per transaction — and nobody puts
// two hundred of those on a slide. What they mean is the total per category.
type Group struct {
	Label string  `json:"label"`
	Text  string  `json:"text"`
	Total float64 `json:"total"`
	Rows  int     `json:"rows"`
}

type Analysis struct {
	Source    SourceRef `json:"source"`
	HasHeader bool      `json:"has_header"`
	RowCount  int       `json:"row_count"`
	Columns   []Column  `json:"columns"`
	Dimension int       `json:"dimension"` // index into Columns, -1 when none
	Measures  []int     `json:"measures"`
	Shape     string    `json:"shape"`
	Chart     string    `json:"chart"`
	Insights  []Insight `json:"insights"`
	Headline  string    `json:"headline"`
	// Groups is set when the rows repeat their category and the measure is one
	// that can be added up. The chart and the findings then speak about the
	// totals, while the table slide still shows the rows themselves.
	Groups  []Group `json:"groups,omitempty"`
	Grouped bool    `json:"grouped"`
}

// Shapes a range can have. The shape decides the slides; the chart decides how
// the numbers are drawn on one of them.
const (
	ShapeEmpty      = "empty"
	ShapeFigures    = "figures"    // a handful of numbers and nothing to plot
	ShapeCategories = "categories" // a name column and one or more measures
	ShapeSeries     = "series"     // a date column and one or more measures
	ShapeTable      = "table"      // too wide or too plain to be anything else
	ShapeSteps      = "steps"      // named stages of a process, in order
	ShapeTimeline   = "timeline"   // dated milestones with nothing to plot
)

// Chart is the picture the range is drawn as — not every one of them plots a
// number. Steps and a timeline draw an order rather than a quantity, and a
// range of dated milestones has nothing to plot but plenty to show.
//
// The names match the components a provider is likely to have, so the mapping
// downstream is a rename rather than a decision.
const (
	ChartNone       = ""
	ChartBars       = "bars"
	ChartLine       = "line"
	ChartShare      = "share"
	ChartComparison = "comparison"
	ChartSteps      = "steps"
	ChartTimeline   = "timeline"
)

// Insight is a sentence the spreadsheet can justify. Every one of these is
// derived from the numbers, never written by a model, so a deck built without
// an AI provider still says something true.
type Insight struct {
	Kind   string  `json:"kind"`
	Label  string  `json:"label"`
	Value  string  `json:"value"`
	Detail string  `json:"detail,omitempty"`
	Number float64 `json:"number"`
}

const (
	InsightTop     = "top"
	InsightBottom  = "bottom"
	InsightTotal   = "total"
	InsightAverage = "average"
	InsightGrowth  = "growth"
	InsightShort   = "short"
)

// Deck is the presentation as meaning: what each slide says, in what form.
// It is deliberately not anybody's file format.
type Deck struct {
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle,omitempty"`
	Language string    `json:"language"`
	Source   SourceRef `json:"source"`
	Slides   []Slide   `json:"slides"`
}

type Slide struct {
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Lead      string     `json:"lead,omitempty"`
	Bullets   []string   `json:"bullets,omitempty"`
	Component *Component `json:"component,omitempty"`
	Notes     string     `json:"notes,omitempty"`
}

const (
	SlideCover   = "cover"
	SlideContent = "content"
	SlideClosing = "closing"
)

type Component struct {
	Kind    string `json:"kind"`
	Caption string `json:"caption,omitempty"`
	Rows    []Row  `json:"rows"`
}

// Row is a component row. Fields beyond the label are whatever that component
// reads: a value and a detail for a KPI tile, a series of numbers for a chart,
// the rest of the row for a table.
type Row struct {
	Label  string   `json:"label"`
	Fields []string `json:"fields,omitempty"`
}
