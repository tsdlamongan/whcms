package contact

// ContactInput is the body of POST /contact: the public "contact us" form
// submitted by an unauthenticated visitor. DepartmentID is optional - when it
// is omitted (0) the ticket is routed to the first active department.
// CaptchaToken is verified only when CAPTCHA is enabled
// (security.captcha_enabled).
type ContactInput struct {
	Name         string `json:"name" validate:"required,min=2,max=100"`
	Email        string `json:"email" validate:"required,email"`
	Subject      string `json:"subject" validate:"required,min=3,max=200"`
	Message      string `json:"message" validate:"required,min=2"`
	DepartmentID int64  `json:"department_id" validate:"omitempty,gt=0"`
	CaptchaToken string `json:"captcha_token"`
}

// ContactResult is the response of POST /contact. Only the ticket number is
// exposed to the anonymous caller (no id/thread).
type ContactResult struct {
	TicketNumber string `json:"ticket_number"`
}
