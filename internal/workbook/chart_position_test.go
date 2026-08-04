package workbook

import (
	"encoding/json"
	"testing"
)

// Browsers report fractional pointer coordinates when the display is scaled, so
// dragging a chart must not fail to save.
func TestChartPositionAcceptsFractionalPixels(t *testing.T) {
	t.Parallel()
	var input UpdateChartInput
	if err := json.Unmarshal([]byte(`{"position":{"x":120.5,"y":90.25,"width":419.6,"height":260.4}}`), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if input.Position == nil {
		t.Fatal("position was not decoded")
	}
	want := ChartPosition{X: 121, Y: 90, Width: 420, Height: 260}
	if *input.Position != want {
		t.Fatalf("position = %#v, want %#v", *input.Position, want)
	}
}

func TestChartPositionRejectsNonNumbers(t *testing.T) {
	t.Parallel()
	var input UpdateChartInput
	if err := json.Unmarshal([]byte(`{"position":{"x":"왼쪽"}}`), &input); err == nil {
		t.Fatal("a text coordinate should still be rejected")
	}
}
