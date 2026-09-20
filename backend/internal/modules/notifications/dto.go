package notifications

// Template variables
//
// Every template is rendered with text/template over a map that always
// contains the global variables:
//
//	{{.CompanyName}}     settings company.name (default "WHCMS")
//	{{.CompanyLogo}}     settings company.logo_key (storage object key, may be "")
//	{{.CompanyAddress}}  settings company.address (may be "")
//	{{.CompanyEmail}}    settings company.email (may be "")
//	{{.FrontendURL}}     config FRONTEND_URL (e.g. https://panel.example.com)
//	{{.Year}}            current year, for the footer copyright line
//
// The special "_layout" template (LayoutTemplateKey) is the global HTML
// wrapper rendered around every other template's body_html. It additionally
// receives:
//
//	{{.Content}}  the rendered per-template body (already escaped)
//
// SendTemplate additionally injects recipient defaults (caller data wins on
// key collision):
//
//	{{.Name}}   client full name, falling back to the user email
//	{{.Email}}  recipient email address
//
// Per-key variables supplied by the calling module (seeded templates):
//
//	verify_email        .VerifyURL
//	reset_password      .ResetURL
//	invoice_created     .InvoiceNumber .Total .DueDate .InvoiceURL
//	invoice_reminder    .InvoiceNumber .Total .DueDate .InvoiceURL
//	invoice_overdue     .InvoiceNumber .Total .DueDate .InvoiceURL
//	payment_received    .InvoiceNumber .Amount
//	service_activated   .ServiceName .Domain .Username .Password .PanelURL
//	service_suspended   .ServiceName .Domain .Reason
//	service_unsuspended .ServiceName .Domain
//	service_terminated  .ServiceName .Domain
//	domain_registered   .Domain .ExpiryDate
//	domain_renewed      .Domain .ExpiryDate
//	domain_profile_incomplete .Domain (sent when registrantContact fails: the
//	                    client profile is missing address/city/state/postcode)
//	ticket_opened       .TicketNumber .Subject .TicketURL
//	ticket_replied      .TicketNumber .Subject .TicketURL
//	admin_alert         .Subject .Detail (sent by AlertAdmin)
//
// Missing keys render as "<no value>" instead of failing the send.

// SaveTemplateInput upserts one email template locale variant.
type SaveTemplateInput struct {
	Key      string `json:"key" validate:"required,max=64"`
	Locale   string `json:"locale" validate:"required,oneof=id en"`
	Subject  string `json:"subject" validate:"required,max=255"`
	BodyHTML string `json:"body_html" validate:"required"`
	BodyText string `json:"body_text" validate:"max=20000"`
}

// SendTestEmailInput is the POST /admin/email/test body.
type SendTestEmailInput struct {
	To string `json:"to" validate:"required,email,max=255"`
}

// TestEmailResult reports a successful synchronous test delivery.
type TestEmailResult struct {
	EmailLogID int64  `json:"email_log_id"`
	To         string `json:"to"`
	Subject    string `json:"subject"`
}

// PreviewInput renders a stored template with sample data.
type PreviewInput struct {
	Key        string         `json:"key" validate:"required,max=64"`
	Locale     string         `json:"locale" validate:"omitempty,oneof=id en"`
	SampleData map[string]any `json:"sample_data"`
}

// PreviewResult is the rendered preview (Key/Locale reflect the template that
// was actually used after locale fallback).
type PreviewResult struct {
	Key      string `json:"key"`
	Locale   string `json:"locale"`
	Subject  string `json:"subject"`
	BodyHTML string `json:"body_html"`
	BodyText string `json:"body_text"`
}
