// Package ui implements the native GTK4 desktop interface.
package ui

import (
	"sync"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/rootxtrem3/VaultKey_Companion/internal/config"
	"github.com/rootxtrem3/VaultKey_Companion/internal/uistate"
)

const applicationID = "io.github.rootxtrem3.VaultKeyCompanion"

// Build identifies this executable in the device diagnostics.
type Build struct {
	Version   string
	Commit    string
	BuildDate string
}

// Run starts the GTK application and returns its process exit status.
func Run(arguments []string, build Build) int {
	application := gtk.NewApplication(applicationID, gio.ApplicationFlagsNone)
	frontend := &frontend{
		application: application,
		build:       build,
		pages:       make(map[string]page),
	}
	application.ConnectActivate(frontend.activate)
	return application.Run(arguments)
}

type page interface {
	Widget() gtk.Widgetter
	Update(uistate.Snapshot)
}

type searchablePage interface {
	FocusSearch()
}

type frontend struct {
	application *gtk.Application
	build       Build

	window     *gtk.ApplicationWindow
	stack      *gtk.Stack
	navigation *gtk.ListBox
	pages      map[string]page

	statusIcon  *gtk.Image
	statusLabel *gtk.Label

	controller *uistate.Controller
	settings   config.Settings

	closeOnce sync.Once
}

func (f *frontend) activate() {
	if f.window != nil {
		f.window.Present()
		return
	}

	settings, settingsErr := config.Load()
	f.settings = settings

	provider := gtk.NewCSSProvider()
	provider.LoadFromData(themeCSS)
	if display := f.application.ActiveWindow(); display != nil {
		// There is no window on the first activation. This branch is useful
		// for subsequent activation paths but the default display below is
		// the normal case.
		gtk.StyleContextAddProviderForDisplay(display.Display(), provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	} else if display := defaultDisplay(); display != nil {
		gtk.StyleContextAddProviderForDisplay(display, provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	}

	window := gtk.NewApplicationWindow(f.application)
	f.window = window
	window.SetTitle("VaultKey Companion")
	window.SetDefaultSize(1120, 720)
	window.SetSizeRequest(840, 560)

	f.controller = uistate.New(uistate.Options{
		Post: func(fn func()) { glib.IdleAdd(fn) },
		Version:          f.build.Version,
		Commit:           f.build.Commit,
		BuildDate:        f.build.BuildDate,
		Timeout:          6 * time.Second,
		DiscoveryTimeout: 15 * time.Second,
	})

	window.SetTitlebar(f.buildHeader())
	window.SetChild(f.buildMainLayout())
	f.installActions()

	f.controller.SetListener(f.update)
	window.ConnectCloseRequest(func() bool {
		f.closeOnce.Do(func() {
			window.SetSensitive(false)
			go func() {
				f.controller.Close()
				glib.IdleAdd(func() {
					window.Destroy()
					f.application.Quit()
				})
			}()
		})
		return true
	})

	window.Present()
	if settingsErr != nil {
		showError(&window.Window, "Could not load application settings", settingsErr)
	}
	f.controller.Start()
}

func (f *frontend) buildHeader() *gtk.HeaderBar {
	header := gtk.NewHeaderBar()
	header.SetShowTitleButtons(true)

	title := gtk.NewBox(gtk.OrientationVertical, 0)
	title.Append(label("VaultKey Companion", "product-name"))
	title.Append(label("Device security utility", "product-sub"))
	header.SetTitleWidget(title)

	statusButton := gtk.NewButton()
	statusButton.SetHasFrame(false)
	statusButton.SetTooltipText("Open USB connection details")
	status := gtk.NewBox(gtk.OrientationHorizontal, 7)
	f.statusIcon = iconImage("network-wired-symbolic", "network-wired-symbolic")
	f.statusIcon.AddCSSClass("status-icon-disconnected")
	f.statusLabel = label("Disconnected", "status-label")
	status.Append(f.statusIcon)
	status.Append(f.statusLabel)
	statusButton.SetChild(status)
	statusButton.ConnectClicked(func() { f.showPage("usb") })
	header.PackEnd(statusButton)

	return header
}

func (f *frontend) buildMainLayout() *gtk.Box {
	root := gtk.NewBox(gtk.OrientationHorizontal, 0)

	navigation := gtk.NewListBox()
	f.navigation = navigation
	navigation.AddCSSClass("nav-rail")
	navigation.SetSelectionMode(gtk.SelectionSingle)
	navigation.SetActivateOnSingleClick(true)
	navigation.SetSizeRequest(196, -1)

	items := []struct {
		name     string
		caption  string
		icon     string
		fallback string
	}{
		{"overview", "Overview", "view-grid-symbolic", "preferences-system-symbolic"},
		{"vault", "Vault", "dialog-password-symbolic", "security-high-symbolic"},
		{"authenticator", "Authenticator", "security-high-symbolic", "dialog-password-symbolic"},
		{"usb", "USB", "drive-removable-media-symbolic", "network-wired-symbolic"},
		{"settings", "Settings", "preferences-system-symbolic", "view-grid-symbolic"},
	}

	for _, item := range items {
		row := gtk.NewListBoxRow()
		row.SetName(item.name)
		row.AddCSSClass("nav-row")
		box := gtk.NewBox(gtk.OrientationHorizontal, 10)
		box.AddCSSClass("nav-item")
		image := iconImage(item.icon, item.fallback)
		image.AddCSSClass("nav-icon")
		box.Append(image)
		box.Append(label(item.caption, ""))
		row.SetChild(box)
		navigation.Append(row)
	}

	stack := gtk.NewStack()
	f.stack = stack
	stack.SetHExpand(true)
	stack.SetVExpand(true)
	stack.SetHhomogeneous(false)
	stack.SetVhomogeneous(false)
	stack.SetTransitionType(gtk.StackTransitionTypeNone)

	f.pages["overview"] = newOverviewPage(f.controller)
	f.pages["vault"] = newVaultPage(f.controller, &f.window.Window)
	f.pages["authenticator"] = newAuthenticatorPage(f.controller, &f.window.Window)
	f.pages["usb"] = newUSBPage(f.controller)
	f.pages["settings"] = newSettingsPage(f.controller, &f.window.Window, f.settings)
	for _, item := range items {
		stack.AddNamed(f.pages[item.name].Widget(), item.name)
	}

	navigation.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row != nil {
			stack.SetVisibleChildName(row.Name())
		}
	})
	navigation.SelectRow(navigation.RowAtIndex(0))

	root.Append(navigation)
	root.Append(stack)
	return root
}

func (f *frontend) installActions() {
	refresh := gio.NewSimpleAction("refresh", nil)
	refresh.ConnectActivate(func(*glib.Variant) { f.controller.Refresh() })
	f.application.AddAction(refresh)
	f.application.SetAccelsForAction("app.refresh", []string{"<Control>r", "F5"})

	search := gio.NewSimpleAction("search", nil)
	search.ConnectActivate(func(*glib.Variant) {
		if f.stack == nil {
			return
		}
		name := f.visiblePageName()
		if page, ok := f.pages[name].(searchablePage); ok {
			page.FocusSearch()
		}
	})
	f.application.AddAction(search)
	f.application.SetAccelsForAction("app.search", []string{"<Control>f"})

	quit := gio.NewSimpleAction("quit", nil)
	quit.ConnectActivate(func(*glib.Variant) {
		if f.window != nil {
			f.window.Close()
		}
	})
	f.application.AddAction(quit)
	f.application.SetAccelsForAction("app.quit", []string{"<Control>q"})
}

func (f *frontend) update(snapshot uistate.Snapshot) {
	if f.window == nil {
		return
	}

	name, fallback := statusIconName(snapshot.State)
	if display := f.window.Display(); display != nil {
		theme := gtk.IconThemeGetForDisplay(display)
		if theme != nil && !theme.HasIcon(name) {
			name = fallback
		}
	}
	f.statusIcon.SetFromIconName(name)
	for _, class := range []string{
		"status-icon-disconnected", "status-icon-active",
		"status-icon-warning", "status-icon-error",
	} {
		f.statusIcon.RemoveCSSClass(class)
	}
	f.statusIcon.AddCSSClass(iconClass(snapshot.State))
	f.statusLabel.SetLabel(snapshot.State.String())

	for _, page := range f.pages {
		page.Update(snapshot)
	}
}

func (f *frontend) showPage(name string) {
	if f.stack == nil || f.navigation == nil {
		return
	}
	for index, candidate := range []string{"overview", "vault", "authenticator", "usb", "settings"} {
		if candidate == name {
			f.navigation.SelectRow(f.navigation.RowAtIndex(index))
			f.stack.SetVisibleChildName(name)
			return
		}
	}
}

func (f *frontend) visiblePageName() string {
	row := f.navigation.SelectedRow()
	if row == nil {
		return ""
	}
	return row.Name()
}
