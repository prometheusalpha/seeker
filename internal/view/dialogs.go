package view

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"

	"seeker/internal/app"
	"seeker/internal/driver"
	"seeker/internal/model"
)

// dialogs are the forms over the window: a connection's fields and a
// server's databases. Both write through to the app as the user types, so
// dismissing one has nothing left to flush.
func (s *App) dialogs(c *ui.Context, pal palette) {
	d := s.core.DialogsState()
	s.connectionDialog(c, pal, &d.Conn)
	s.databasesDialog(c, pal, &d.Databases)
	s.closeDialog(c)
	s.filesDialog(c)
	s.quickOpen(c)
}

func (s *App) connectionDialog(c *ui.Context, pal palette, d *app.ConnDialog) {
	// The form's derived fields settle once, before it builds: the TLS mode
	// from the parameters, an engine's defaults when the type changed, and
	// a test result that no longer matches what is typed.
	s.core.ConnSync()
	open := d.Open
	ui.Modal(c, &open, func() {
		pg := d.Kind == app.KindPostgres
		title := "New connection"
		if d.Editing != "" {
			title = "Edit connection"
		}
		databasePlaceholder, paramsPlaceholder, namePlaceholder := "mysql", "charset=utf8mb4", "Local MySQL"
		if pg {
			databasePlaceholder, paramsPlaceholder, namePlaceholder = "postgres", "sslmode=verify-full&application_name=seeker", "Local Postgres"
		}
		ui.Column(c).Width(560).Padding(20).Gap(14).Children(func() {
			ui.Text(c, title).FontSize(16).Bold()
			ui.Scroll(c).FillWidth().MaxHeight(460).Children(func() {
				ui.Column(c).FillWidth().Gap(12).Children(func() {
					ui.Field(c, "Name", func() {
						ui.TextInput(c, &d.Name).Placeholder(namePlaceholder)
					})
					ui.Field(c, "Type", func() {
						ui.Select(c, &d.Kind, app.ConnectionKinds).FillWidth().Label("Type")
					})
					ui.Row(c).FillWidth().Gap(10).Children(func() {
						ui.Field(c, "Host", func() {
							ui.TextInput(c, &d.Host)
						}).Grow(1)
						ui.Field(c, "Port", func() {
							ui.TextInput(c, &d.Port)
						}).Width(110)
					})
					ui.Row(c).FillWidth().Gap(10).Children(func() {
						ui.Field(c, "User", func() {
							ui.TextInput(c, &d.User)
						}).Grow(1)
						ui.Field(c, "Password", func() {
							ui.TextInput(c, &d.Password).Password()
						}).Grow(1)
					})
					database := ui.Field(c, "Database", func() {
						ui.TextInput(c, &d.Database).Placeholder(databasePlaceholder)
					})
					// The test is what fills the list, as recall's datalists
					// are filled: the field stays free text.
					if len(d.Databases) > 0 {
						database.Description(fmt.Sprintf("%d databases found; press Test connection to refresh.", len(d.Databases)))
					}
					if pg {
						schema := ui.Field(c, "Default schema", func() {
							ui.TextInput(c, &d.Schema)
						})
						if len(d.Schemas) > 0 {
							schema.Description(fmt.Sprintf("%d schemas found; press Test connection to refresh.", len(d.Schemas)))
						}
					}
					ui.Field(c, "URL parameters", func() {
						ui.TextInput(c, &d.Params).Placeholder(paramsPlaceholder)
					})
					ui.Field(c, "TLS mode", func() { s.tlsModeSelect(c, d) })
					ui.Field(c, "Note", func() {
						ui.TextArea(c, &d.Note).Height(60).Placeholder("What this connection is for")
					})
					ui.Row(c).FillWidth().Gap(10).Children(func() {
						ui.Field(c, "Connect timeout", func() {
							ui.TextInput(c, &d.ConnectTimeout).Placeholder("10")
						}).Grow(1).Description("Seconds; 0 uses the driver's 30-second default.")
						ui.Field(c, "Query timeout", func() {
							ui.TextInput(c, &d.QueryTimeout).Placeholder("0")
						}).Grow(1).Description("Seconds; 0 means no limit.")
					})
					if pg {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
							ui.Text(c, "Show system schemas").FontSize(12)
							ui.Spacer(c)
							ui.Switch(c, &d.ShowSystem).Label("Show system schemas")
						})
					}
					ui.Checkbox(c, &d.SavePassword, "Save password in the keychain")
				})
			})
			s.connTest(c, pal, d)
			s.connFooter(c, d)
		})
	})
	if !open && d.Open {
		s.core.CloseConnectionDialog()
	}
}

// tlsModeSelect shows the modes as labels while the form holds the mode
// the drivers read, so choosing one rewrites the URL parameters at once.
func (s *App) tlsModeSelect(c *ui.Context, d *app.ConnDialog) {
	modes, labels := driver.TLSModes(engine(d.Kind)), driver.TLSModeLabels(engine(d.Kind))
	choice := labels[0]
	if i := slices.Index(modes, d.TLS); i >= 0 {
		choice = labels[i]
	}
	if ui.Select(c, &choice, labels).FillWidth().Label("TLS mode").Changed() {
		if i := slices.Index(labels, choice); i >= 0 {
			d.TLS = modes[i]
			s.core.ConnTLSChanged()
		}
	}
}

// connTest is what the last test said: a spinner while it runs, the
// server's version once it passes, and the whole error, copyable, when it
// fails. A failed test is the only place the server's text survives
// unsummarized.
func (s *App) connTest(c *ui.Context, pal palette, d *app.ConnDialog) {
	t := c.Theme()
	switch {
	case d.Test.Pending:
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Spinner(c).FontSize(12)
			ui.Text(c, "Testing…").FontSize(12).TextColor(t.TextMuted)
		})
	case d.Test.OK:
		ui.Text(c, d.Test.Detail).FontSize(12).TextColor(t.TextMuted).SingleLine()
	case d.Test.Failure != "":
		ui.Column(c).FillWidth().Gap(8).Padding(10).Radius(6).Background(pal.Active).Border(1, t.Border).Children(func() {
			ui.Text(c, "Connection failed").FontSize(11).Bold().TextColor(t.TextMuted)
			ui.Scroll(c).FillWidth().MaxHeight(160).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					ui.Text(c, d.Test.Failure).FontSize(11).TextColor(t.TextMuted).Label("Full error message")
				})
			})
			ui.Row(c).FillWidth().Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "Copy").Clicked() {
					c.WriteClipboard(d.Test.Failure)
					c.Toast("Error copied to clipboard")
				}
			})
		})
	}
}

func (s *App) connFooter(c *ui.Context, d *app.ConnDialog) {
	t := c.Theme()
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		if d.Err != "" {
			ui.Text(c, d.Err).FontSize(12).TextColor(t.Danger).SingleLine()
		}
		ui.Spacer(c)
		// Test and save are exclusive: both use the form as it stands, and
		// neither may run over the other.
		pending := d.Test.Pending
		if ui.Button(c, "Cancel").Disabled(pending).Clicked() {
			s.core.CloseConnectionDialog()
		}
		if ui.Button(c, "Test connection").Disabled(pending).Clicked() {
			s.core.TestConnectionDialog()
		}
		if ui.PrimaryButton(c, "Save & connect").Disabled(pending || d.PasswordLoading).Clicked() {
			s.core.SaveConnectionDialog()
		}
	})
}

func (s *App) databasesDialog(c *ui.Context, pal palette, d *app.DatabasesDialog) {
	t := c.Theme()
	open := d.Open
	ui.Modal(c, &open, func() {
		items := s.core.DatabaseItems()
		// A filtered list that came up empty still has databases behind it;
		// only an unfiltered empty list means the server listed none.
		empty := len(items) == 0 && d.Filter == ""
		ui.Column(c).Width(480).Padding(20).Gap(12).Children(func() {
			ui.Text(c, "Databases").FontSize(16).Bold()
			ui.Text(c, "Hidden databases are left out of the sidebar; open tabs keep working.").
				FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &d.Filter).Placeholder("Filter databases").
				Label("Filter databases").Disabled(d.Loading || empty)
			switch {
			case d.Loading:
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Spinner(c).FontSize(12)
					ui.Text(c, "Opening connection…").FontSize(12).TextColor(t.TextMuted)
				})
			case d.Err != "":
				ui.Column(c).FillWidth().Gap(8).Padding(10).Radius(6).Background(pal.Active).Border(1, t.Border).Children(func() {
					ui.Text(c, d.Err).FontSize(12).TextColor(t.Danger)
					ui.Row(c).FillWidth().Children(func() {
						ui.Spacer(c)
						if ui.Button(c, "Retry").Clicked() {
							s.core.OpenDatabasesDialog(d.ConnID)
						}
					})
				})
			case empty:
				ui.Text(c, "This server listed no databases.").FontSize(12).TextColor(t.TextMuted)
			case len(items) == 0:
				ui.Textf(c, "No database matches “%s”.", d.Filter).FontSize(12).TextColor(t.TextMuted)
			default:
				ui.Scroll(c).FillWidth().MaxHeight(320).Children(func() {
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						for _, item := range items {
							s.databaseRow(c, t, item)
						}
					})
				})
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				nothing := d.Loading || empty
				if ui.Button(c, "Show all").Disabled(nothing).Clicked() {
					s.core.ShowAllDatabases()
				}
				if ui.Button(c, "Hide all").Disabled(nothing).Clicked() {
					s.core.HideAllDatabases()
				}
				ui.Spacer(c)
				if ui.Button(c, "Done").Clicked() {
					s.core.CloseDatabasesDialog()
				}
			})
		})
	})
	if !open && d.Open {
		s.core.CloseDatabasesDialog()
	}
}

// databaseRow shows one database and the switch that hides it. The switch
// writes a copy of the row's state, since the list itself belongs to the
// app, and reports through ToggleDatabase, which saves at once.
func (s *App) databaseRow(c *ui.Context, t *ui.Theme, item app.DatabaseItem) {
	hidden := item.Hidden
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Padding(5, 8).Radius(6).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			name := ui.Text(c, item.Name).FontSize(12).SingleLine()
			if hidden {
				name.TextColor(t.TextMuted)
			}
			switch {
			case item.Own:
				ui.Text(c, "Default database, always shown").FontSize(11).TextColor(t.TextMuted).SingleLine()
			case item.Comment != "":
				ui.Text(c, item.Comment).FontSize(11).TextColor(t.TextMuted).SingleLine()
			}
		})
		if ui.Switch(c, &hidden).Label(item.Name).Disabled(item.Own).Changed() {
			s.core.ToggleDatabase(item.Name)
		}
	})
}

// engine is the driver a dialog's type names.
func engine(kind string) model.DBType {
	if kind == app.KindMySQL {
		return model.MySQL
	}
	return model.Postgres
}
