package ui

import (
	"context"
	"image"
	"slices"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"gon/internal/fontset"
)

type fontField int

const (
	terminalFont fontField = iota
	interfaceFont
)

type fontSettings struct {
	selectors [2]widget.Clickable
	directory widget.Clickable
	menuOpen  bool
	field     fontField
	choices   []fontset.Choice
	buttons   []widget.Clickable
	list      widget.List
	download  *fontDownload
	reload    [2]bool
}

type fontDownload struct {
	field  fontField
	value  string
	result chan error
	cancel context.CancelFunc
}

func (s *fontSettings) cancelDownload() {
	if s.download != nil {
		s.download.cancel()
		s.download = nil
	}
}

func (s *settingsPanel) fontValue(field fontField) *string {
	if field == interfaceFont {
		return &s.draft.InterfaceFont.Family
	}
	return &s.draft.Font.Family
}

func defaultFontLabel(field fontField) string {
	if field == interfaceFont {
		return "跟随终端"
	}
	return "默认字体"
}

func (a *App) updateFontSettings(gtx layout.Context) {
	s := &a.settings
	f := &s.fonts
	if f.directory.Clicked(gtx) {
		if err := showFontDirectory(a.fontDirectory); err != nil {
			s.problem = "打开字体目录失败　" + err.Error()
		}
	}
	for i := range f.selectors {
		if !f.selectors[i].Clicked(gtx) || f.download != nil {
			continue
		}
		field := fontField(i)
		choices, err := fontset.Choices(a.fontDirectory)
		if err != nil {
			s.problem = "读取字体目录失败　" + err.Error()
		}
		choices = append([]fontset.Choice{{Label: defaultFontLabel(field)}}, choices...)
		current := *s.fontValue(field)
		index := slices.IndexFunc(choices, func(c fontset.Choice) bool { return c.Value == current })
		if index == -1 {
			choices = slices.Insert(choices, 1, fontset.Choice{Value: current, Label: fontset.ChoiceName(current)})
			index = 1
		}
		f.field, f.menuOpen = field, true
		f.choices = choices
		f.buttons = make([]widget.Clickable, len(choices))
		f.list = widget.List{List: layout.List{Axis: layout.Vertical}}
		if index >= 6 {
			f.list.ScrollTo(index)
		}
	}
}

func (a *App) drawFontPicker(gtx layout.Context, r image.Rectangle, field fontField, label string) image.Rectangle {
	s := &a.settings
	px, _ := a.drawSettingLabel(gtx, r, label)
	h := gtx.Dp(30)
	y := r.Min.Y + (r.Dy()-h)/2
	w := r.Dx() - min(gtx.Dp(96), r.Dx()/3)
	button := image.Rect(r.Max.X-w, y, r.Max.X, y+h)
	name := fontset.ChoiceName(*s.fontValue(field))
	if name == "" {
		name = defaultFontLabel(field)
	}
	if s.fonts.download != nil {
		gtx = gtx.Disabled()
		if s.fonts.download.field == field {
			name = "正在下载…"
		}
	}
	a.drawSettingsButton(gtx, button, &s.fonts.selectors[field], a.textFit(name, px, w-gtx.Dp(24))+" ▾", a.theme.PanelHi)
	return button
}

type fontMenuBackdrop struct{}

func (a *App) drawFontMenu(gtx layout.Context, area, anchor image.Rectangle) {
	s := &a.settings
	f := &s.fonts
	if _, ok := gtx.Event(pointer.Filter{Target: fontMenuBackdrop{}, Kinds: pointer.Press}); ok {
		f.menuOpen = false
		return
	}
	for i := range f.buttons {
		if f.buttons[i].Clicked(gtx) {
			a.chooseFont(f.choices[i])
			return
		}
	}
	if anchor.Empty() {
		return
	}
	inset, rowHeight := gtx.Dp(5), gtx.Dp(29)
	width := min(area.Dx()-2*inset, max(anchor.Dx(), gtx.Dp(296)))
	left := max(area.Min.X+inset, anchor.Max.X-width)
	above, below := anchor.Min.Y-area.Min.Y-inset*2, area.Max.Y-anchor.Max.Y-inset*2
	height := min(len(f.choices)*rowHeight, max(above, below))
	if width <= 0 || height <= 0 {
		return
	}
	y := anchor.Max.Y + inset
	if height > below {
		y = anchor.Min.Y - inset - height
	}
	r := image.Rect(left, y, left+width, y+height)
	// Catch outside clicks while the list is open without intercepting its scroll area.
	for _, outside := range []image.Rectangle{
		image.Rect(area.Min.X, area.Min.Y, area.Max.X, r.Min.Y),
		image.Rect(area.Min.X, r.Max.Y, area.Max.X, area.Max.Y),
		image.Rect(area.Min.X, r.Min.Y, r.Min.X, r.Max.Y),
		image.Rect(r.Max.X, r.Min.Y, area.Max.X, r.Max.Y),
	} {
		stack := clip.Rect(outside).Push(gtx.Ops)
		event.Op(gtx.Ops, fontMenuBackdrop{})
		stack.Pop()
	}
	paint.FillShape(gtx.Ops, NRGBA(lighten(a.theme.Panel, 0.13), 0xff), clip.UniformRRect(r.Inset(-1), inset).Op(gtx.Ops))
	defer clip.UniformRRect(r, inset).Push(gtx.Ops).Pop()
	defer op.Offset(r.Min).Push(gtx.Ops).Pop()
	paint.FillShape(gtx.Ops, NRGBA(a.theme.Panel, 0xff), clip.Rect(image.Rectangle{Max: r.Size()}).Op())
	gtx.Constraints = layout.Exact(r.Size())
	f.list.Layout(gtx, len(f.choices), func(gtx layout.Context, i int) layout.Dimensions {
		choice := f.choices[i]
		name := choice.Label
		if choice.Download {
			name += " · 下载"
		}
		px := a.uiPx(gtx, 13)
		bg := condColor(choice.Value == *s.fontValue(f.field), mix(a.theme.Panel, a.theme.Accent, 0.22), 0)
		row := image.Rect(inset, 0, width-inset, rowHeight)
		a.drawSettingsButton(gtx, row, &f.buttons[i], a.textFit(name, px, row.Dx()-2*inset), bg)
		return layout.Dimensions{Size: image.Pt(width, rowHeight)}
	})
}

func (a *App) chooseFont(choice fontset.Choice) {
	s := &a.settings
	f := &s.fonts
	f.menuOpen = false
	s.problem = ""
	if !choice.Download {
		*s.fontValue(f.field) = choice.Value
		a.invalidate()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &fontDownload{field: f.field, value: choice.Value, result: make(chan error, 1), cancel: cancel}
	f.download = job
	go func() {
		job.result <- fontset.InstallChoice(ctx, a.fontDirectory, job.value)
		a.invalidate()
	}()
}

func (s *settingsPanel) receiveFontDownload() {
	job := s.fonts.download
	if job == nil {
		return
	}
	select {
	case err := <-job.result:
		s.fonts.cancelDownload()
		if err != nil {
			s.problem = "下载字体失败　" + err.Error()
		} else {
			s.problem = ""
			*s.fontValue(job.field) = job.value
			s.fonts.reload[job.field] = true
		}
	default:
	}
}
