package tickets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/tickets"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	transporthttp "github.com/tsdlamongan/whcms/backend/internal/transport/http"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"
	"github.com/tsdlamongan/whcms/backend/pkg/httpx"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// Fakes

// fakeMW injects a fixed identity and mirrors production role/permission
// semantics.
type fakeMW struct {
	identity    httpx.AuthIdentity
	permissions map[string]bool
}

func (m *fakeMW) RequireAuth() fiber.Handler {
	return func(c fiber.Ctx) error {
		httpx.SetIdentity(c, m.identity)
		return c.Next()
	}
}

func (m *fakeMW) RequireRole(roles ...string) fiber.Handler {
	allowed := map[string]bool{}
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c fiber.Ctx) error {
		id, _ := httpx.Identity(c)
		if !allowed[id.Role] {
			return apperr.Forbidden("insufficient role")
		}
		return c.Next()
	}
}

func (m *fakeMW) RequirePermission(module string) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, _ := httpx.Identity(c)
		if id.Role == "admin" {
			return c.Next()
		}
		if !m.permissions[module] {
			return apperr.Forbidden("missing permission: " + module)
		}
		return c.Next()
	}
}

func (m *fakeMW) RequireClient() fiber.Handler {
	return func(c fiber.Ctx) error {
		id, _ := httpx.Identity(c)
		if id.ClientID == 0 {
			return apperr.Forbidden("client profile required")
		}
		return c.Next()
	}
}

// fakeSvc implements tickets.TicketService with function fields.
type fakeSvc struct {
	CreateTicketFn     func(ctx context.Context, clientID int64, in tickets.CreateTicketInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error)
	ListMineFn         func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error)
	GetMineFn          func(ctx context.Context, clientID, ticketID int64) (*tickets.TicketDetail, error)
	ReplyMineFn        func(ctx context.Context, clientID, ticketID int64, in tickets.ReplyInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error)
	CloseMineFn        func(ctx context.Context, clientID, ticketID int64) (*domain.Ticket, error)
	AdminListFn        func(ctx context.Context, f tickets.AdminListFilter) ([]domain.Ticket, int64, error)
	AdminGetFn         func(ctx context.Context, ticketID int64) (*tickets.TicketDetail, error)
	AssignFn           func(ctx context.Context, actorUserID, ticketID int64, in tickets.AssignInput) (*domain.Ticket, error)
	AdminReplyFn       func(ctx context.Context, actorUserID, ticketID int64, in tickets.ReplyInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error)
	SetStatusFn        func(ctx context.Context, actorUserID, ticketID int64, in tickets.SetStatusInput) (*domain.Ticket, error)
	SetPriorityFn      func(ctx context.Context, actorUserID, ticketID int64, in tickets.SetPriorityInput) (*domain.Ticket, error)
	AttachmentURLFn    func(ctx context.Context, clientID, ticketID int64, idx int) (string, error)
	ListDepartmentsFn  func(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error)
	CreateDepartmentFn func(ctx context.Context, actorUserID int64, in tickets.DepartmentInput) (*domain.TicketDepartment, error)
	UpdateDepartmentFn func(ctx context.Context, actorUserID, id int64, in tickets.DepartmentInput) (*domain.TicketDepartment, error)
	DeleteDepartmentFn func(ctx context.Context, actorUserID, id int64) error
}

func (f *fakeSvc) CreateTicket(ctx context.Context, clientID int64, in tickets.CreateTicketInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error) {
	if f.CreateTicketFn != nil {
		return f.CreateTicketFn(ctx, clientID, in, files)
	}
	return &tickets.TicketDetail{}, nil
}

func (f *fakeSvc) ListMine(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error) {
	if f.ListMineFn != nil {
		return f.ListMineFn(ctx, clientID, p)
	}
	return nil, 0, nil
}

func (f *fakeSvc) GetMine(ctx context.Context, clientID, ticketID int64) (*tickets.TicketDetail, error) {
	if f.GetMineFn != nil {
		return f.GetMineFn(ctx, clientID, ticketID)
	}
	return &tickets.TicketDetail{}, nil
}

func (f *fakeSvc) ReplyMine(ctx context.Context, clientID, ticketID int64, in tickets.ReplyInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error) {
	if f.ReplyMineFn != nil {
		return f.ReplyMineFn(ctx, clientID, ticketID, in, files)
	}
	return &tickets.TicketDetail{}, nil
}

func (f *fakeSvc) CloseMine(ctx context.Context, clientID, ticketID int64) (*domain.Ticket, error) {
	if f.CloseMineFn != nil {
		return f.CloseMineFn(ctx, clientID, ticketID)
	}
	return &domain.Ticket{}, nil
}

func (f *fakeSvc) AdminList(ctx context.Context, fl tickets.AdminListFilter) ([]domain.Ticket, int64, error) {
	if f.AdminListFn != nil {
		return f.AdminListFn(ctx, fl)
	}
	return nil, 0, nil
}

func (f *fakeSvc) AdminGet(ctx context.Context, ticketID int64) (*tickets.TicketDetail, error) {
	if f.AdminGetFn != nil {
		return f.AdminGetFn(ctx, ticketID)
	}
	return &tickets.TicketDetail{}, nil
}

func (f *fakeSvc) Assign(ctx context.Context, actorUserID, ticketID int64, in tickets.AssignInput) (*domain.Ticket, error) {
	if f.AssignFn != nil {
		return f.AssignFn(ctx, actorUserID, ticketID, in)
	}
	return &domain.Ticket{}, nil
}

func (f *fakeSvc) AdminReply(ctx context.Context, actorUserID, ticketID int64, in tickets.ReplyInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error) {
	if f.AdminReplyFn != nil {
		return f.AdminReplyFn(ctx, actorUserID, ticketID, in, files)
	}
	return &tickets.TicketDetail{}, nil
}

func (f *fakeSvc) SetStatus(ctx context.Context, actorUserID, ticketID int64, in tickets.SetStatusInput) (*domain.Ticket, error) {
	if f.SetStatusFn != nil {
		return f.SetStatusFn(ctx, actorUserID, ticketID, in)
	}
	return &domain.Ticket{}, nil
}

func (f *fakeSvc) SetPriority(ctx context.Context, actorUserID, ticketID int64, in tickets.SetPriorityInput) (*domain.Ticket, error) {
	if f.SetPriorityFn != nil {
		return f.SetPriorityFn(ctx, actorUserID, ticketID, in)
	}
	return &domain.Ticket{}, nil
}

func (f *fakeSvc) AttachmentURL(ctx context.Context, clientID, ticketID int64, idx int) (string, error) {
	if f.AttachmentURLFn != nil {
		return f.AttachmentURLFn(ctx, clientID, ticketID, idx)
	}
	return "", nil
}

func (f *fakeSvc) ListDepartments(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error) {
	if f.ListDepartmentsFn != nil {
		return f.ListDepartmentsFn(ctx, activeOnly)
	}
	return nil, nil
}

func (f *fakeSvc) CreateDepartment(ctx context.Context, actorUserID int64, in tickets.DepartmentInput) (*domain.TicketDepartment, error) {
	if f.CreateDepartmentFn != nil {
		return f.CreateDepartmentFn(ctx, actorUserID, in)
	}
	return &domain.TicketDepartment{}, nil
}

func (f *fakeSvc) UpdateDepartment(ctx context.Context, actorUserID, id int64, in tickets.DepartmentInput) (*domain.TicketDepartment, error) {
	if f.UpdateDepartmentFn != nil {
		return f.UpdateDepartmentFn(ctx, actorUserID, id, in)
	}
	return &domain.TicketDepartment{}, nil
}

func (f *fakeSvc) DeleteDepartment(ctx context.Context, actorUserID, id int64) error {
	if f.DeleteDepartmentFn != nil {
		return f.DeleteDepartmentFn(ctx, actorUserID, id)
	}
	return nil
}

var _ tickets.TicketService = (*fakeSvc)(nil)

func newApp(svc tickets.TicketService, id httpx.AuthIdentity, perms map[string]bool) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: transporthttp.ErrorHandler(discard)})
	h := tickets.NewHandler(svc, &fakeMW{identity: id, permissions: perms})
	h.RegisterRoutes(app.Group("/api/v1"))
	return app
}

func clientApp(svc tickets.TicketService) *fiber.App {
	return newApp(svc, httpx.AuthIdentity{UserID: 70, Role: "client", ClientID: 7}, nil)
}

func adminApp(svc tickets.TicketService) *fiber.App {
	return newApp(svc, httpx.AuthIdentity{UserID: 1, Role: "admin"}, nil)
}

func readEnvelope(t *testing.T, body io.Reader) httpx.Envelope {
	t.Helper()
	var env httpx.Envelope
	require.NoError(t, json.NewDecoder(body).Decode(&env))
	return env
}

func multipartBody(t *testing.T, fields map[string]string, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	for name, content := range files {
		fw, err := w.CreateFormFile("attachments", name)
		require.NoError(t, err)
		_, err = fw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf, w.FormDataContentType()
}

// Client routes

func TestHandlerCreateTicketMultipart(t *testing.T) {
	var gotClient int64
	var gotIn tickets.CreateTicketInput
	var gotFiles []tickets.AttachmentUpload
	var gotContents []string
	svc := &fakeSvc{
		CreateTicketFn: func(ctx context.Context, clientID int64, in tickets.CreateTicketInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error) {
			gotClient, gotIn, gotFiles = clientID, in, files
			for _, f := range files {
				b, _ := io.ReadAll(f.Reader)
				gotContents = append(gotContents, string(b))
			}
			return &tickets.TicketDetail{Ticket: domain.Ticket{ID: 11, TicketNumber: "TKT-000001"}}, nil
		},
	}
	app := clientApp(svc)

	body, contentType := multipartBody(t,
		map[string]string{"department_id": "3", "subject": "Website down", "priority": "high", "message": "Help"},
		map[string]string{"shot.png": "png-bytes"})
	req := httptest.NewRequest("POST", "/api/v1/tickets/", body)
	req.Header.Set("Content-Type", contentType)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)

	assert.Equal(t, int64(7), gotClient)
	assert.Equal(t, int64(3), gotIn.DepartmentID)
	assert.Equal(t, "Website down", gotIn.Subject)
	assert.Equal(t, "high", gotIn.Priority)
	assert.Equal(t, "Help", gotIn.Message)
	require.Len(t, gotFiles, 1)
	assert.Equal(t, "shot.png", gotFiles[0].Filename)
	assert.Equal(t, int64(len("png-bytes")), gotFiles[0].Size)
	assert.Equal(t, []string{"png-bytes"}, gotContents)
}

func TestHandlerCreateTicketJSON(t *testing.T) {
	var gotIn tickets.CreateTicketInput
	svc := &fakeSvc{
		CreateTicketFn: func(ctx context.Context, clientID int64, in tickets.CreateTicketInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error) {
			gotIn = in
			assert.Empty(t, files)
			return &tickets.TicketDetail{}, nil
		},
	}
	app := clientApp(svc)
	req := httptest.NewRequest("POST", "/api/v1/tickets/",
		strings.NewReader(`{"department_id":3,"subject":"Hi","message":"Text"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)
	assert.Equal(t, int64(3), gotIn.DepartmentID)
}

func TestHandlerCreateTicketRequiresClient(t *testing.T) {
	app := newApp(&fakeSvc{}, httpx.AuthIdentity{UserID: 1, Role: "admin"}, nil)
	req := httptest.NewRequest("POST", "/api/v1/tickets/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestHandlerMyList(t *testing.T) {
	svc := &fakeSvc{
		ListMineFn: func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error) {
			assert.Equal(t, int64(7), clientID)
			assert.Equal(t, "open", p.Status)
			assert.Equal(t, 2, p.Page)
			return []domain.Ticket{{ID: 1, TicketNumber: "TKT-000001"}}, 26, nil
		},
	}
	resp, err := clientApp(svc).Test(httptest.NewRequest("GET", "/api/v1/tickets/?status=open&page=2", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := readEnvelope(t, resp.Body)
	require.NotNil(t, env.Meta)
	assert.Equal(t, int64(26), env.Meta.Total)
	assert.Equal(t, 2, env.Meta.Page)
}

func TestHandlerMyDetailAndInvalidID(t *testing.T) {
	svc := &fakeSvc{
		GetMineFn: func(ctx context.Context, clientID, ticketID int64) (*tickets.TicketDetail, error) {
			assert.Equal(t, int64(11), ticketID)
			return &tickets.TicketDetail{Ticket: domain.Ticket{ID: 11}}, nil
		},
	}
	app := clientApp(svc)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/tickets/11", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/tickets/abc", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerMyDetailNotFoundPassthrough(t *testing.T) {
	svc := &fakeSvc{
		GetMineFn: func(ctx context.Context, clientID, ticketID int64) (*tickets.TicketDetail, error) {
			return nil, apperr.NotFound("ticket")
		},
	}
	resp, err := clientApp(svc).Test(httptest.NewRequest("GET", "/api/v1/tickets/11", nil))
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode)
}

func TestHandlerMyReplyMultipart(t *testing.T) {
	var gotIn tickets.ReplyInput
	var nFiles int
	svc := &fakeSvc{
		ReplyMineFn: func(ctx context.Context, clientID, ticketID int64, in tickets.ReplyInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error) {
			gotIn = in
			nFiles = len(files)
			return &tickets.TicketDetail{}, nil
		},
	}
	body, contentType := multipartBody(t, map[string]string{"message": "more info"},
		map[string]string{"log.txt": "log data"})
	req := httptest.NewRequest("POST", "/api/v1/tickets/11/replies", body)
	req.Header.Set("Content-Type", contentType)
	resp, err := clientApp(svc).Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)
	assert.Equal(t, "more info", gotIn.Message)
	assert.Equal(t, 1, nFiles)
}

func TestHandlerMyClose(t *testing.T) {
	svc := &fakeSvc{
		CloseMineFn: func(ctx context.Context, clientID, ticketID int64) (*domain.Ticket, error) {
			assert.Equal(t, int64(7), clientID)
			assert.Equal(t, int64(11), ticketID)
			return &domain.Ticket{ID: 11, Status: domain.TicketClosed}, nil
		},
	}
	resp, err := clientApp(svc).Test(httptest.NewRequest("POST", "/api/v1/tickets/11/close", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
}

func TestHandlerMyAttachmentRedirect(t *testing.T) {
	svc := &fakeSvc{
		AttachmentURLFn: func(ctx context.Context, clientID, ticketID int64, idx int) (string, error) {
			assert.Equal(t, int64(7), clientID)
			assert.Equal(t, int64(11), ticketID)
			assert.Equal(t, 1, idx)
			return "https://s3.example/presigned", nil
		},
	}
	resp, err := clientApp(svc).Test(httptest.NewRequest("GET", "/api/v1/tickets/11/attachments/1", nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "https://s3.example/presigned", resp.Header.Get("Location"))
}

func TestHandlerMyAttachmentInvalidIdx(t *testing.T) {
	resp, err := clientApp(&fakeSvc{}).Test(httptest.NewRequest("GET", "/api/v1/tickets/11/attachments/x", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

// Public

func TestHandlerPublicDepartments(t *testing.T) {
	svc := &fakeSvc{
		ListDepartmentsFn: func(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error) {
			assert.True(t, activeOnly)
			return []domain.TicketDepartment{{ID: 1, Name: "Support"}}, nil
		},
	}
	// no auth middleware runs for the public route: use a zero identity app
	app := newApp(svc, httpx.AuthIdentity{}, nil)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/ticket-departments", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	env := readEnvelope(t, resp.Body)
	require.Nil(t, env.Error)
	require.Len(t, env.Data.([]any), 1)
}

// Admin routes

func TestHandlerAdminListFilters(t *testing.T) {
	var got tickets.AdminListFilter
	svc := &fakeSvc{
		AdminListFn: func(ctx context.Context, f tickets.AdminListFilter) ([]domain.Ticket, int64, error) {
			got = f
			return nil, 0, nil
		},
	}
	resp, err := adminApp(svc).Test(httptest.NewRequest("GET",
		"/api/v1/admin/tickets/?status=open&priority=high&department_id=3&assigned_user_id=55&client_id=9&search=down&page=2&per_page=10", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "open", got.Status)
	assert.Equal(t, "high", got.Priority)
	assert.Equal(t, int64(3), got.DepartmentID)
	assert.Equal(t, int64(55), got.AssignedUserID)
	assert.Equal(t, int64(9), got.ClientID)
	assert.Equal(t, "down", got.Search)
	assert.Equal(t, 2, got.Page)
	assert.Equal(t, 10, got.PerPage)
}

func TestHandlerAdminListRejectsInvalidClientID(t *testing.T) {
	called := false
	svc := &fakeSvc{
		AdminListFn: func(ctx context.Context, f tickets.AdminListFilter) ([]domain.Ticket, int64, error) {
			called = true
			return nil, 0, nil
		},
	}
	resp, err := adminApp(svc).Test(httptest.NewRequest("GET", "/api/v1/admin/tickets/?client_id=abc", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
	assert.False(t, called, "a malformed client_id must never widen the list")
}

func TestHandlerAdminRoutesRequireStaffPermission(t *testing.T) {
	// staff WITHOUT the support permission
	app := newApp(&fakeSvc{}, httpx.AuthIdentity{UserID: 5, Role: "staff"}, map[string]bool{})
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/tickets/", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)

	// staff WITH the permission
	app = newApp(&fakeSvc{}, httpx.AuthIdentity{UserID: 5, Role: "staff"}, map[string]bool{"support": true})
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/tickets/", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// client role can never reach admin routes
	app = newApp(&fakeSvc{}, httpx.AuthIdentity{UserID: 70, Role: "client", ClientID: 7}, nil)
	resp, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/tickets/", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestHandlerAdminAssign(t *testing.T) {
	var gotActor, gotTicket int64
	var gotIn tickets.AssignInput
	svc := &fakeSvc{
		AssignFn: func(ctx context.Context, actorUserID, ticketID int64, in tickets.AssignInput) (*domain.Ticket, error) {
			gotActor, gotTicket, gotIn = actorUserID, ticketID, in
			return &domain.Ticket{ID: ticketID}, nil
		},
	}
	req := httptest.NewRequest("POST", "/api/v1/admin/tickets/11/assign",
		strings.NewReader(`{"assigned_user_id":55}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := adminApp(svc).Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(1), gotActor)
	assert.Equal(t, int64(11), gotTicket)
	assert.Equal(t, int64(55), gotIn.AssignedUserID)
}

func TestHandlerAdminReplyInternalMultipart(t *testing.T) {
	var gotIn tickets.ReplyInput
	svc := &fakeSvc{
		AdminReplyFn: func(ctx context.Context, actorUserID, ticketID int64, in tickets.ReplyInput, files []tickets.AttachmentUpload) (*tickets.TicketDetail, error) {
			gotIn = in
			return &tickets.TicketDetail{}, nil
		},
	}
	body, contentType := multipartBody(t,
		map[string]string{"message": "internal note", "is_internal": "true"}, nil)
	req := httptest.NewRequest("POST", "/api/v1/admin/tickets/11/replies", body)
	req.Header.Set("Content-Type", contentType)
	resp, err := adminApp(svc).Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)
	assert.True(t, gotIn.IsInternal)
	assert.Equal(t, "internal note", gotIn.Message)
}

func TestHandlerAdminSetStatus(t *testing.T) {
	var gotIn tickets.SetStatusInput
	svc := &fakeSvc{
		SetStatusFn: func(ctx context.Context, actorUserID, ticketID int64, in tickets.SetStatusInput) (*domain.Ticket, error) {
			gotIn = in
			return &domain.Ticket{ID: ticketID, Status: domain.TicketStatus(in.Status)}, nil
		},
	}
	req := httptest.NewRequest("POST", "/api/v1/admin/tickets/11/status",
		strings.NewReader(`{"status":"on_hold"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := adminApp(svc).Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "on_hold", gotIn.Status)
}

func TestHandlerAdminSetPriority(t *testing.T) {
	var gotActor, gotTicket int64
	var gotIn tickets.SetPriorityInput
	svc := &fakeSvc{
		SetPriorityFn: func(ctx context.Context, actorUserID, ticketID int64, in tickets.SetPriorityInput) (*domain.Ticket, error) {
			gotActor, gotTicket, gotIn = actorUserID, ticketID, in
			return &domain.Ticket{ID: ticketID, Priority: domain.TicketPriority(in.Priority)}, nil
		},
	}
	req := httptest.NewRequest("POST", "/api/v1/admin/tickets/11/priority",
		strings.NewReader(`{"priority":"low"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := adminApp(svc).Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(1), gotActor)
	assert.Equal(t, int64(11), gotTicket)
	assert.Equal(t, "low", gotIn.Priority)

	// bad id and bad body are rejected before reaching the service
	resp, err = adminApp(svc).Test(httptest.NewRequest("POST", "/api/v1/admin/tickets/abc/priority", strings.NewReader(`{}`)))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)

	req = httptest.NewRequest("POST", "/api/v1/admin/tickets/11/priority", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = adminApp(svc).Test(req)
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestHandlerAdminSetPriorityServiceError(t *testing.T) {
	svc := &fakeSvc{
		SetPriorityFn: func(ctx context.Context, actorUserID, ticketID int64, in tickets.SetPriorityInput) (*domain.Ticket, error) {
			return nil, apperr.Conflict("ticket already has priority high")
		},
	}
	req := httptest.NewRequest("POST", "/api/v1/admin/tickets/11/priority", strings.NewReader(`{"priority":"high"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := adminApp(svc).Test(req)
	require.NoError(t, err)
	assert.Equal(t, 409, resp.StatusCode)
}

func TestHandlerAdminAttachmentStaffPath(t *testing.T) {
	var gotClient int64 = -1
	svc := &fakeSvc{
		AttachmentURLFn: func(ctx context.Context, clientID, ticketID int64, idx int) (string, error) {
			gotClient = clientID
			return "https://s3.example/x", nil
		},
	}
	resp, err := adminApp(svc).Test(httptest.NewRequest("GET", "/api/v1/admin/tickets/11/attachments/0", nil))
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, int64(0), gotClient, "staff path passes clientID 0 (no ownership filter)")
}

// Departments admin

func TestHandlerDepartmentsCRUD(t *testing.T) {
	var created tickets.DepartmentInput
	var updatedID int64
	var deletedID int64
	svc := &fakeSvc{
		ListDepartmentsFn: func(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error) {
			assert.False(t, activeOnly)
			return []domain.TicketDepartment{{ID: 1}}, nil
		},
		CreateDepartmentFn: func(ctx context.Context, actorUserID int64, in tickets.DepartmentInput) (*domain.TicketDepartment, error) {
			created = in
			return &domain.TicketDepartment{ID: 8, Name: in.Name}, nil
		},
		UpdateDepartmentFn: func(ctx context.Context, actorUserID, id int64, in tickets.DepartmentInput) (*domain.TicketDepartment, error) {
			updatedID = id
			return &domain.TicketDepartment{ID: id, Name: in.Name}, nil
		},
		DeleteDepartmentFn: func(ctx context.Context, actorUserID, id int64) error {
			deletedID = id
			return nil
		},
	}
	app := adminApp(svc)

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/ticket-departments/", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	req := httptest.NewRequest("POST", "/api/v1/admin/ticket-departments/",
		strings.NewReader(`{"name":"Billing","email":"billing@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)
	assert.Equal(t, "Billing", created.Name)

	req = httptest.NewRequest("PATCH", "/api/v1/admin/ticket-departments/8",
		strings.NewReader(`{"name":"Billing & Sales"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int64(8), updatedID)

	resp, err = app.Test(httptest.NewRequest("DELETE", "/api/v1/admin/ticket-departments/8", nil))
	require.NoError(t, err)
	assert.Equal(t, 204, resp.StatusCode)
	assert.Equal(t, int64(8), deletedID)
}

func TestHandlerDepartmentsAdminOnly(t *testing.T) {
	// staff (even with support permission) cannot manage departments
	app := newApp(&fakeSvc{}, httpx.AuthIdentity{UserID: 5, Role: "staff"}, map[string]bool{"support": true})
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/ticket-departments/", nil))
	require.NoError(t, err)
	assert.Equal(t, 403, resp.StatusCode)
}

func TestHandlerDeleteDepartmentConflictPassthrough(t *testing.T) {
	svc := &fakeSvc{
		DeleteDepartmentFn: func(ctx context.Context, actorUserID, id int64) error {
			return apperr.Conflict("department has tickets and cannot be deleted")
		},
	}
	resp, err := adminApp(svc).Test(httptest.NewRequest("DELETE", "/api/v1/admin/ticket-departments/3", nil))
	require.NoError(t, err)
	assert.Equal(t, 409, resp.StatusCode)
	env := readEnvelope(t, resp.Body)
	assert.Equal(t, "CONFLICT", env.Error.Code)
}

func TestHandlerInvalidJSONBodies(t *testing.T) {
	app := adminApp(&fakeSvc{})
	for _, path := range []string{
		"/api/v1/admin/tickets/11/assign",
		"/api/v1/admin/tickets/11/status",
		"/api/v1/admin/ticket-departments/",
	} {
		req := httptest.NewRequest("POST", path, strings.NewReader("not-json"))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		require.NoError(t, err, path)
		assert.Equal(t, 422, resp.StatusCode, path)
	}
}

// AdminListFilter paging defaults, as bound from the admin list query
// params.
func TestAdminListFilterPaging(t *testing.T) {
	f := tickets.AdminListFilter{}
	assert.Equal(t, 25, f.Limit())
	assert.Equal(t, 0, f.Offset())

	f = tickets.AdminListFilter{Page: 3, PerPage: 10}
	assert.Equal(t, 10, f.Limit())
	assert.Equal(t, 20, f.Offset())

	// the page-size selector offers up to 1000 rows (CONTRACTS §3)
	f = tickets.AdminListFilter{Page: 2, PerPage: 500}
	assert.Equal(t, 500, f.Limit())
	assert.Equal(t, 500, f.Offset())

	f = tickets.AdminListFilter{Page: 2, PerPage: 5000}
	assert.Equal(t, 1000, f.Limit(), "clamped to ports.MaxPerPage")
}

func TestHandlerAdminDetail(t *testing.T) {
	svc := &fakeSvc{
		AdminGetFn: func(ctx context.Context, ticketID int64) (*tickets.TicketDetail, error) {
			assert.Equal(t, int64(11), ticketID)
			return &tickets.TicketDetail{Ticket: domain.Ticket{ID: 11}}, nil
		},
	}
	resp, err := adminApp(svc).Test(httptest.NewRequest("GET", "/api/v1/admin/tickets/11", nil))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = adminApp(svc).Test(httptest.NewRequest("GET", "/api/v1/admin/tickets/abc", nil))
	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}
