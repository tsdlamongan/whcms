package tickets_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/tickets"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/internal/ports/mocks"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

// fakeSearch implements tickets.AdminSearchStore.
type fakeSearch struct {
	fn func(ctx context.Context, f tickets.AdminListFilter) ([]domain.Ticket, int64, error)
}

func (s *fakeSearch) ListAdmin(ctx context.Context, f tickets.AdminListFilter) ([]domain.Ticket, int64, error) {
	if s.fn != nil {
		return s.fn(ctx, f)
	}
	return nil, 0, nil
}

// env bundles the mocked dependencies for one test.
type env struct {
	tickets  *mocks.MockTicketRepo
	search   *fakeSearch
	clients  *mocks.MockClientRepo
	users    *mocks.MockUserRepo
	storage  *mocks.MockStorage
	settings *mocks.MockSettingsRepo
	notifier *mocks.MockNotificationSender
	audit    *mocks.MockAuditLogger
}

func newFixture() *env {
	return &env{
		tickets:  &mocks.MockTicketRepo{},
		search:   &fakeSearch{},
		clients:  &mocks.MockClientRepo{},
		users:    &mocks.MockUserRepo{},
		storage:  &mocks.MockStorage{},
		settings: &mocks.MockSettingsRepo{},
		notifier: &mocks.MockNotificationSender{},
		audit:    &mocks.MockAuditLogger{},
	}
}

func (e *env) service() *tickets.Service {
	return tickets.New(tickets.Deps{
		Tx:          &mocks.MockTxManager{},
		Tickets:     e.tickets,
		Search:      e.search,
		Clients:     e.clients,
		Users:       e.users,
		Storage:     e.storage,
		Settings:    e.settings,
		Notifier:    e.notifier,
		Audit:       e.audit,
		Clock:       &mocks.MockClock{FixedTime: fixedNow},
		FrontendURL: "http://front.test/",
	})
}

func ptr[T any](v T) *T { return &v }

func activeDept() *domain.TicketDepartment {
	return &domain.TicketDepartment{ID: 3, Name: "Support", Email: "support@example.com", Active: true}
}

func testClient() *domain.Client {
	return &domain.Client{ID: 7, UserID: 70, FirstName: "Budi", LastName: "Santoso"}
}

func openTicket() *domain.Ticket {
	return &domain.Ticket{
		ID: 11, TicketNumber: "TKT-000042", ClientID: ptr(int64(7)),
		DepartmentID: 3, Subject: "Website down", Status: domain.TicketOpen,
		Priority: domain.PriorityHigh,
	}
}

func upload(name, content string) tickets.AttachmentUpload {
	return tickets.AttachmentUpload{
		Filename:    name,
		Size:        int64(len(content)),
		ContentType: "application/octet-stream",
		Reader:      strings.NewReader(content),
	}
}

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	e := apperr.From(err)
	assert.Equal(t, code, e.Code)
}

// CreateTicket

func TestCreateTicketHappyPathWithAttachments(t *testing.T) {
	e := newFixture()
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		require.Equal(t, int64(3), id)
		return activeDept(), nil
	}
	e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) {
		require.Equal(t, int64(7), id)
		return testClient(), nil
	}
	e.tickets.NextNumberFn = func(ctx context.Context, scope string) (int64, error) {
		assert.Equal(t, domain.TicketCounterScope, scope)
		return 42, nil
	}
	var created *domain.Ticket
	e.tickets.CreateFn = func(ctx context.Context, tk *domain.Ticket) error {
		tk.ID = 11
		created = tk
		return nil
	}
	var putKeys []string
	e.storage.PutFn = func(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
		putKeys = append(putKeys, key)
		body, _ := io.ReadAll(r)
		assert.Equal(t, int(size), len(body))
		return nil
	}
	var reply *domain.TicketReply
	e.tickets.AddReplyFn = func(ctx context.Context, r *domain.TicketReply) error {
		r.ID = 21
		reply = r
		return nil
	}
	var templates []string
	e.notifier.SendTemplateFn = func(ctx context.Context, userID int64, key string, data map[string]any) error {
		assert.Equal(t, int64(70), userID)
		assert.Equal(t, "TKT-000042", data["TicketNumber"])
		assert.Equal(t, "http://front.test/support/11", data["TicketURL"])
		templates = append(templates, key)
		return nil
	}
	var alerts []string
	e.notifier.AlertAdminFn = func(ctx context.Context, subject, message string) error {
		alerts = append(alerts, subject)
		return nil
	}

	detail, err := e.service().CreateTicket(context.Background(), 7, tickets.CreateTicketInput{
		DepartmentID: 3, Subject: "Website down", Priority: "high", Message: "Please help",
	}, []tickets.AttachmentUpload{upload("screen shot.PNG", "img-bytes")})
	require.NoError(t, err)

	require.NotNil(t, created)
	assert.Equal(t, "TKT-000042", created.TicketNumber)
	assert.Equal(t, domain.TicketOpen, created.Status)
	assert.Equal(t, domain.PriorityHigh, created.Priority)
	require.NotNil(t, created.LastReplyAt)
	assert.Equal(t, fixedNow, *created.LastReplyAt)

	require.Len(t, putKeys, 1)
	assert.True(t, strings.HasPrefix(putKeys[0], "tickets/TKT-000042/"), putKeys[0])
	assert.True(t, strings.HasSuffix(putKeys[0], "-screen_shot.PNG"), putKeys[0])

	require.NotNil(t, reply)
	assert.Equal(t, "Budi Santoso", reply.AuthorName)
	assert.False(t, reply.IsInternal)
	var metas []domain.TicketAttachment
	require.NoError(t, json.Unmarshal(reply.Attachments, &metas))
	require.Len(t, metas, 1)
	assert.Equal(t, putKeys[0], metas[0].ObjectKey)
	assert.Equal(t, int64(len("img-bytes")), metas[0].Size)

	assert.Equal(t, []string{"ticket_opened"}, templates)
	require.Len(t, alerts, 1)
	assert.Contains(t, alerts[0], "TKT-000042")

	require.NotNil(t, detail)
	assert.Equal(t, "TKT-000042", detail.Ticket.TicketNumber)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "ticket.create", e.audit.Entries[0].Action)
	assert.Equal(t, int64(70), e.audit.Entries[0].ActorUserID)
	assert.Equal(t, int64(11), e.audit.Entries[0].EntityID)
}

func TestCreateTicketDefaultsPriorityMedium(t *testing.T) {
	e := newFixture()
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) { return testClient(), nil }
	e.tickets.NextNumberFn = func(ctx context.Context, scope string) (int64, error) { return 1, nil }
	var created *domain.Ticket
	e.tickets.CreateFn = func(ctx context.Context, tk *domain.Ticket) error { created = tk; return nil }

	_, err := e.service().CreateTicket(context.Background(), 7, tickets.CreateTicketInput{
		DepartmentID: 3, Subject: "Need info", Message: "Hello there",
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.PriorityMedium, created.Priority)
}

func TestCreateTicketValidation(t *testing.T) {
	tests := []struct {
		name string
		in   tickets.CreateTicketInput
	}{
		{"missing subject", tickets.CreateTicketInput{DepartmentID: 3, Message: "hello there"}},
		{"missing message", tickets.CreateTicketInput{DepartmentID: 3, Subject: "subject"}},
		{"bad priority", tickets.CreateTicketInput{DepartmentID: 3, Subject: "subject", Priority: "urgent", Message: "hello"}},
		{"missing department", tickets.CreateTicketInput{Subject: "subject", Message: "hello"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newFixture().service().CreateTicket(context.Background(), 7, tc.in, nil)
			requireCode(t, err, apperr.CodeValidation)
		})
	}
}

func TestCreateTicketInactiveDepartment(t *testing.T) {
	e := newFixture()
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		d := activeDept()
		d.Active = false
		return d, nil
	}
	_, err := e.service().CreateTicket(context.Background(), 7, tickets.CreateTicketInput{
		DepartmentID: 3, Subject: "subject ok", Message: "message ok",
	}, nil)
	requireCode(t, err, apperr.CodeValidation)
}

func TestCreateTicketDepartmentNotFound(t *testing.T) {
	e := newFixture()
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return nil, apperr.NotFound("department")
	}
	_, err := e.service().CreateTicket(context.Background(), 7, tickets.CreateTicketInput{
		DepartmentID: 3, Subject: "subject ok", Message: "message ok",
	}, nil)
	requireCode(t, err, apperr.CodeNotFound)
}

func TestCreateTicketAttachmentRules(t *testing.T) {
	base := tickets.CreateTicketInput{DepartmentID: 3, Subject: "subject ok", Message: "message ok"}

	newSvcEnv := func() *env {
		e := newFixture()
		e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
			return activeDept(), nil
		}
		return e
	}

	t.Run("disallowed extension", func(t *testing.T) {
		_, err := newSvcEnv().service().CreateTicket(context.Background(), 7, base,
			[]tickets.AttachmentUpload{upload("virus.exe", "x")})
		requireCode(t, err, apperr.CodeValidation)
	})

	t.Run("no extension", func(t *testing.T) {
		_, err := newSvcEnv().service().CreateTicket(context.Background(), 7, base,
			[]tickets.AttachmentUpload{upload("README", "x")})
		requireCode(t, err, apperr.CodeValidation)
	})

	t.Run("oversize", func(t *testing.T) {
		e := newSvcEnv()
		e.settings.GetIntFn = func(ctx context.Context, key string, def int) (int, error) {
			assert.Equal(t, "tickets.max_attachment_mb", key)
			return 1, nil
		}
		f := upload("big.pdf", "x")
		f.Size = 2 * 1024 * 1024
		_, err := e.service().CreateTicket(context.Background(), 7, base, []tickets.AttachmentUpload{f})
		requireCode(t, err, apperr.CodeValidation)
	})

	t.Run("empty file", func(t *testing.T) {
		f := upload("empty.pdf", "")
		_, err := newSvcEnv().service().CreateTicket(context.Background(), 7, base, []tickets.AttachmentUpload{f})
		requireCode(t, err, apperr.CodeValidation)
	})

	t.Run("too many files", func(t *testing.T) {
		files := make([]tickets.AttachmentUpload, 6)
		for i := range files {
			files[i] = upload("a.png", "x")
		}
		_, err := newSvcEnv().service().CreateTicket(context.Background(), 7, base, files)
		requireCode(t, err, apperr.CodeValidation)
	})

	t.Run("extensions overridden from settings", func(t *testing.T) {
		e := newSvcEnv()
		e.settings.GetJSONFn = func(ctx context.Context, key string, out any) error {
			require.Equal(t, "tickets.allowed_extensions", key)
			*(out.(*[]string)) = []string{"exe"}
			return nil
		}
		e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) { return testClient(), nil }
		e.tickets.NextNumberFn = func(ctx context.Context, scope string) (int64, error) { return 5, nil }
		_, err := e.service().CreateTicket(context.Background(), 7, base,
			[]tickets.AttachmentUpload{upload("tool.exe", "x")})
		require.NoError(t, err)
	})
}

func TestCreateTicketStorageFailureAbortsTx(t *testing.T) {
	e := newFixture()
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) { return testClient(), nil }
	e.tickets.NextNumberFn = func(ctx context.Context, scope string) (int64, error) { return 9, nil }
	e.storage.PutFn = func(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
		return errors.New("s3 down")
	}
	replied := false
	e.tickets.AddReplyFn = func(ctx context.Context, r *domain.TicketReply) error { replied = true; return nil }

	_, err := e.service().CreateTicket(context.Background(), 7, tickets.CreateTicketInput{
		DepartmentID: 3, Subject: "subject ok", Message: "message ok",
	}, []tickets.AttachmentUpload{upload("ok.png", "x")})
	requireCode(t, err, apperr.CodeInternal)
	assert.False(t, replied, "reply must not be written when upload fails")
}

// TestCreateTicketAttachmentContentTypeIgnoresClientHeader is a regression
// test for the stored-XSS risk where a malicious client uploads a ".txt" file
// (an allowed extension) but spoofs the multipart Content-Type header as
// "text/html" so a browser would render the attachment instead of downloading
// it. The service must derive the stored content type from the file
// extension only, never from the client-supplied header.
func TestCreateTicketAttachmentContentTypeIgnoresClientHeader(t *testing.T) {
	e := newFixture()
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) { return testClient(), nil }
	e.tickets.NextNumberFn = func(ctx context.Context, scope string) (int64, error) { return 1, nil }

	var putContentType string
	e.storage.PutFn = func(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
		putContentType = contentType
		return nil
	}
	var reply *domain.TicketReply
	e.tickets.AddReplyFn = func(ctx context.Context, r *domain.TicketReply) error { reply = r; return nil }

	f := upload("notes.txt", "<script>alert(1)</script>")
	f.ContentType = "text/html" // spoofed by the malicious client

	_, err := e.service().CreateTicket(context.Background(), 7, tickets.CreateTicketInput{
		DepartmentID: 3, Subject: "subject ok", Message: "message ok",
	}, []tickets.AttachmentUpload{f})
	require.NoError(t, err)

	assert.Equal(t, "text/plain; charset=utf-8", putContentType,
		"stored object content-type must be derived from the extension, not the spoofed client header")

	var metas []domain.TicketAttachment
	require.NoError(t, json.Unmarshal(reply.Attachments, &metas))
	require.Len(t, metas, 1)
	assert.Equal(t, "text/plain; charset=utf-8", metas[0].ContentType)
}

// CreateGuestTicket

func TestCreateGuestTicket(t *testing.T) {
	e := newFixture()
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.tickets.NextNumberFn = func(ctx context.Context, scope string) (int64, error) { return 1, nil }
	var created *domain.Ticket
	e.tickets.CreateFn = func(ctx context.Context, tk *domain.Ticket) error { tk.ID = 99; created = tk; return nil }
	var alerts []string
	e.notifier.AlertAdminFn = func(ctx context.Context, subject, message string) error {
		alerts = append(alerts, subject)
		return nil
	}

	ticket, err := e.service().CreateGuestTicket(context.Background(), ports.GuestTicketInput{
		DepartmentID: 3, GuestName: "Visitor", GuestEmail: "visitor@example.com",
		Subject: "Question", Message: "Hi there",
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Nil(t, created.ClientID)
	require.Len(t, alerts, 1)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "ticket.create", e.audit.Entries[0].Action)
	assert.Equal(t, int64(0), e.audit.Entries[0].ActorUserID, "guest ticket has no user actor")
	assert.Equal(t, ticket.ID, e.audit.Entries[0].EntityID)
}

// Client list / detail / reply / close

func TestListMineInvalidStatus(t *testing.T) {
	_, _, err := newFixture().service().ListMine(context.Background(), 7, ports.ListParams{Status: "bogus"})
	requireCode(t, err, apperr.CodeValidation)
}

func TestListMinePassthrough(t *testing.T) {
	e := newFixture()
	e.tickets.ListByClientFn = func(ctx context.Context, clientID int64, p ports.ListParams) ([]domain.Ticket, int64, error) {
		assert.Equal(t, int64(7), clientID)
		assert.Equal(t, "open", p.Status)
		return []domain.Ticket{*openTicket()}, 1, nil
	}
	rows, total, err := e.service().ListMine(context.Background(), 7, ports.ListParams{Status: "open"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
}

func TestGetMineHidesInternalNotes(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	var gotInternal *bool
	e.tickets.ListRepliesFn = func(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.TicketReply, error) {
		gotInternal = &includeInternal
		return []domain.TicketReply{
			{ID: 1, AuthorName: "Budi", Message: "help", Attachments: json.RawMessage(`[{"object_key":"tickets/TKT-000042/a-x.png","filename":"x.png","size":3,"content_type":"image/png"}]`)},
		}, nil
	}
	detail, err := e.service().GetMine(context.Background(), 7, 11)
	require.NoError(t, err)
	require.NotNil(t, gotInternal)
	assert.False(t, *gotInternal, "client detail must exclude internal notes")
	require.Len(t, detail.Replies, 1)
	require.Len(t, detail.Replies[0].Attachments, 1)
	assert.Equal(t, 0, detail.Replies[0].Attachments[0].Index)
	assert.Equal(t, "x.png", detail.Replies[0].Attachments[0].Filename)
	require.NotNil(t, detail.Department)
}

func TestGetMineOtherClientIs404(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) {
		tk := openTicket()
		tk.ClientID = ptr(int64(99))
		return tk, nil
	}
	_, err := e.service().GetMine(context.Background(), 7, 11)
	requireCode(t, err, apperr.CodeNotFound)
}

func TestGetMineNilClientIs404(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) {
		tk := openTicket()
		tk.ClientID = nil
		return tk, nil
	}
	_, err := e.service().GetMine(context.Background(), 7, 11)
	requireCode(t, err, apperr.CodeNotFound)
}

func TestReplyMineSetsCustomerReplyAndNotifiesAssigned(t *testing.T) {
	e := newFixture()
	tk := openTicket()
	tk.Status = domain.TicketAnswered
	tk.AssignedUserID = ptr(int64(55))
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return tk, nil }
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) { return testClient(), nil }
	var updated *domain.Ticket
	e.tickets.UpdateFn = func(ctx context.Context, t *domain.Ticket) error { updated = t; return nil }
	var reply *domain.TicketReply
	e.tickets.AddReplyFn = func(ctx context.Context, r *domain.TicketReply) error { reply = r; return nil }
	var notifiedUser int64
	e.notifier.SendTemplateFn = func(ctx context.Context, userID int64, key string, data map[string]any) error {
		notifiedUser = userID
		assert.Equal(t, "ticket_replied", key)
		assert.Equal(t, "http://front.test/admin/tickets/11", data["TicketURL"])
		return nil
	}
	alerted := false
	e.notifier.AlertAdminFn = func(ctx context.Context, subject, message string) error { alerted = true; return nil }

	_, err := e.service().ReplyMine(context.Background(), 7, 11,
		tickets.ReplyInput{Message: "still broken", IsInternal: true}, nil)
	require.NoError(t, err)

	require.NotNil(t, updated)
	assert.Equal(t, domain.TicketCustomerReply, updated.Status)
	require.NotNil(t, reply)
	assert.False(t, reply.IsInternal, "client reply can never be internal")
	assert.Equal(t, int64(55), notifiedUser)
	assert.False(t, alerted)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "ticket.reply", e.audit.Entries[0].Action)
	assert.Equal(t, int64(70), e.audit.Entries[0].ActorUserID)
}

func TestReplyMineUnassignedAlertsAdmin(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) { return testClient(), nil }
	sent := false
	e.notifier.SendTemplateFn = func(ctx context.Context, userID int64, key string, data map[string]any) error {
		sent = true
		return nil
	}
	alerted := false
	e.notifier.AlertAdminFn = func(ctx context.Context, subject, message string) error { alerted = true; return nil }

	_, err := e.service().ReplyMine(context.Background(), 7, 11, tickets.ReplyInput{Message: "any update?"}, nil)
	require.NoError(t, err)
	assert.True(t, alerted)
	assert.False(t, sent)
}

func TestReplyMineClosedTicketConflict(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) {
		tk := openTicket()
		tk.Status = domain.TicketClosed
		return tk, nil
	}
	_, err := e.service().ReplyMine(context.Background(), 7, 11, tickets.ReplyInput{Message: "hello?"}, nil)
	requireCode(t, err, apperr.CodeConflict)
}

func TestReplyMineValidation(t *testing.T) {
	_, err := newFixture().service().ReplyMine(context.Background(), 7, 11, tickets.ReplyInput{Message: ""}, nil)
	requireCode(t, err, apperr.CodeValidation)
}

func TestCloseMine(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) { return testClient(), nil }
	var updated *domain.Ticket
	e.tickets.UpdateFn = func(ctx context.Context, t *domain.Ticket) error { updated = t; return nil }

	got, err := e.service().CloseMine(context.Background(), 7, 11)
	require.NoError(t, err)
	assert.Equal(t, domain.TicketClosed, got.Status)
	require.NotNil(t, updated.ClosedAt)
	assert.Equal(t, fixedNow, *updated.ClosedAt)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "ticket.close", e.audit.Entries[0].Action)
	assert.Equal(t, int64(70), e.audit.Entries[0].ActorUserID)
}

func TestCloseMineAlreadyClosed(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) {
		tk := openTicket()
		tk.Status = domain.TicketClosed
		return tk, nil
	}
	_, err := e.service().CloseMine(context.Background(), 7, 11)
	requireCode(t, err, apperr.CodeConflict)
}

func TestCloseMineNotOwner(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) {
		tk := openTicket()
		tk.ClientID = ptr(int64(1))
		return tk, nil
	}
	_, err := e.service().CloseMine(context.Background(), 7, 11)
	requireCode(t, err, apperr.CodeNotFound)
}

// Staff/admin

func TestAdminListPassthroughAndValidation(t *testing.T) {
	e := newFixture()
	e.search.fn = func(ctx context.Context, f tickets.AdminListFilter) ([]domain.Ticket, int64, error) {
		assert.Equal(t, "open", f.Status)
		assert.Equal(t, int64(3), f.DepartmentID)
		return []domain.Ticket{*openTicket()}, 1, nil
	}
	rows, total, err := e.service().AdminList(context.Background(), tickets.AdminListFilter{Status: "open", DepartmentID: 3})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)

	_, _, err = e.service().AdminList(context.Background(), tickets.AdminListFilter{Status: "bogus"})
	requireCode(t, err, apperr.CodeValidation)
}

func TestAdminGetIncludesInternalNotes(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	var gotInternal bool
	e.tickets.ListRepliesFn = func(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.TicketReply, error) {
		gotInternal = includeInternal
		return []domain.TicketReply{{ID: 1, IsInternal: true, Message: "internal note"}}, nil
	}
	detail, err := e.service().AdminGet(context.Background(), 11)
	require.NoError(t, err)
	assert.True(t, gotInternal)
	require.Len(t, detail.Replies, 1)
	assert.True(t, detail.Replies[0].IsInternal)
}

func TestAssign(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	e.users.GetByIDFn = func(ctx context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Role: domain.RoleStaff, Email: "staff@example.com"}, nil
	}
	var updated *domain.Ticket
	e.tickets.UpdateFn = func(ctx context.Context, t *domain.Ticket) error { updated = t; return nil }

	got, err := e.service().Assign(context.Background(), 1, 11, tickets.AssignInput{AssignedUserID: 55})
	require.NoError(t, err)
	require.NotNil(t, got.AssignedUserID)
	assert.Equal(t, int64(55), *got.AssignedUserID)
	require.NotNil(t, updated)
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "ticket.assign", e.audit.Entries[0].Action)
}

func TestAssignUnassign(t *testing.T) {
	e := newFixture()
	tk := openTicket()
	tk.AssignedUserID = ptr(int64(55))
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return tk, nil }
	got, err := e.service().Assign(context.Background(), 1, 11, tickets.AssignInput{AssignedUserID: 0})
	require.NoError(t, err)
	assert.Nil(t, got.AssignedUserID)
}

func TestAssignClientUserRejected(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	e.users.GetByIDFn = func(ctx context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Role: domain.RoleClient}, nil
	}
	_, err := e.service().Assign(context.Background(), 1, 11, tickets.AssignInput{AssignedUserID: 70})
	requireCode(t, err, apperr.CodeValidation)
}

func TestAdminReplyPublicAnswersAndNotifiesClient(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.users.GetByIDFn = func(ctx context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Role: domain.RoleStaff, Email: "staff@example.com"}, nil
	}
	e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) { return testClient(), nil }
	var updated *domain.Ticket
	e.tickets.UpdateFn = func(ctx context.Context, t *domain.Ticket) error { updated = t; return nil }
	var reply *domain.TicketReply
	e.tickets.AddReplyFn = func(ctx context.Context, r *domain.TicketReply) error { reply = r; return nil }
	var notified int64
	e.notifier.SendTemplateFn = func(ctx context.Context, userID int64, key string, data map[string]any) error {
		notified = userID
		assert.Equal(t, "ticket_replied", key)
		return nil
	}

	_, err := e.service().AdminReply(context.Background(), 5, 11, tickets.ReplyInput{Message: "fixed now"}, nil)
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, domain.TicketAnswered, updated.Status)
	assert.Equal(t, "staff@example.com", reply.AuthorName)
	assert.Equal(t, int64(70), notified)

	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "ticket.reply", e.audit.Entries[0].Action)
	assert.Equal(t, int64(5), e.audit.Entries[0].ActorUserID)
}

func TestAdminReplyInternalNoteKeepsStatusAndSilence(t *testing.T) {
	e := newFixture()
	tk := openTicket()
	tk.Status = domain.TicketCustomerReply
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return tk, nil }
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.users.GetByIDFn = func(ctx context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Role: domain.RoleAdmin, Email: "admin@example.com"}, nil
	}
	updateCalled := false
	e.tickets.UpdateFn = func(ctx context.Context, t *domain.Ticket) error { updateCalled = true; return nil }
	var reply *domain.TicketReply
	e.tickets.AddReplyFn = func(ctx context.Context, r *domain.TicketReply) error { reply = r; return nil }
	sent := false
	e.notifier.SendTemplateFn = func(ctx context.Context, userID int64, key string, data map[string]any) error {
		sent = true
		return nil
	}

	_, err := e.service().AdminReply(context.Background(), 5, 11,
		tickets.ReplyInput{Message: "note to self", IsInternal: true}, nil)
	require.NoError(t, err)
	assert.True(t, reply.IsInternal)
	assert.False(t, updateCalled, "internal note must not change status")
	assert.False(t, sent, "internal note must not notify the client")
	assert.Equal(t, domain.TicketCustomerReply, tk.Status)
}

func TestAdminReplyClosedTicket(t *testing.T) {
	e := newFixture()
	tk := openTicket()
	tk.Status = domain.TicketClosed
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return tk, nil }
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.users.GetByIDFn = func(ctx context.Context, id int64) (*domain.User, error) {
		return &domain.User{ID: id, Role: domain.RoleStaff}, nil
	}

	_, err := e.service().AdminReply(context.Background(), 5, 11, tickets.ReplyInput{Message: "public reply"}, nil)
	requireCode(t, err, apperr.CodeConflict)

	// internal notes are allowed on closed tickets
	_, err = e.service().AdminReply(context.Background(), 5, 11,
		tickets.ReplyInput{Message: "internal ok", IsInternal: true}, nil)
	require.NoError(t, err)
}

func TestSetStatus(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	var updated *domain.Ticket
	e.tickets.UpdateFn = func(ctx context.Context, t *domain.Ticket) error { updated = t; return nil }

	got, err := e.service().SetStatus(context.Background(), 1, 11, tickets.SetStatusInput{Status: "closed"})
	require.NoError(t, err)
	assert.Equal(t, domain.TicketClosed, got.Status)
	require.NotNil(t, updated.ClosedAt)
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "ticket.status", e.audit.Entries[0].Action)
}

func TestSetStatusReopenClearsClosedAt(t *testing.T) {
	e := newFixture()
	tk := openTicket()
	tk.Status = domain.TicketClosed
	tk.ClosedAt = &fixedNow
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return tk, nil }
	got, err := e.service().SetStatus(context.Background(), 1, 11, tickets.SetStatusInput{Status: "open"})
	require.NoError(t, err)
	assert.Equal(t, domain.TicketOpen, got.Status)
	assert.Nil(t, got.ClosedAt)
}

func TestSetStatusNoOpConflictAndInvalid(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	_, err := e.service().SetStatus(context.Background(), 1, 11, tickets.SetStatusInput{Status: "open"})
	requireCode(t, err, apperr.CodeConflict)

	_, err = e.service().SetStatus(context.Background(), 1, 11, tickets.SetStatusInput{Status: "bogus"})
	requireCode(t, err, apperr.CodeValidation)
}

func TestSetPriority(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	var updated *domain.Ticket
	e.tickets.UpdateFn = func(ctx context.Context, t *domain.Ticket) error { updated = t; return nil }

	got, err := e.service().SetPriority(context.Background(), 1, 11, tickets.SetPriorityInput{Priority: "low"})
	require.NoError(t, err)
	assert.Equal(t, domain.PriorityLow, got.Priority)
	assert.Equal(t, domain.PriorityLow, updated.Priority)
	require.Len(t, e.audit.Entries, 1)
	assert.Equal(t, "ticket.priority", e.audit.Entries[0].Action)
}

func TestSetPriorityNoOpConflictInvalidAndFailures(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }

	// openTicket() is already "high"
	_, err := e.service().SetPriority(context.Background(), 1, 11, tickets.SetPriorityInput{Priority: "high"})
	requireCode(t, err, apperr.CodeConflict)

	_, err = e.service().SetPriority(context.Background(), 1, 11, tickets.SetPriorityInput{Priority: "urgent"})
	requireCode(t, err, apperr.CodeValidation)

	// store failure on update is surfaced, and nothing is audited
	e.tickets.UpdateFn = func(ctx context.Context, t *domain.Ticket) error { return errors.New("db down") }
	_, err = e.service().SetPriority(context.Background(), 1, 11, tickets.SetPriorityInput{Priority: "low"})
	require.Error(t, err)
	assert.Empty(t, e.audit.Entries)

	// unknown ticket
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return nil, apperr.NotFound("ticket") }
	_, err = e.service().SetPriority(context.Background(), 1, 99, tickets.SetPriorityInput{Priority: "low"})
	requireCode(t, err, apperr.CodeNotFound)
}

// Attachments

func repliesWithAttachments() []domain.TicketReply {
	return []domain.TicketReply{
		{ID: 1, Attachments: json.RawMessage(`[{"object_key":"tickets/TKT-000042/u1-a.png","filename":"a.png","size":1,"content_type":"image/png"}]`)},
		{ID: 2, Attachments: json.RawMessage(`[{"object_key":"tickets/TKT-000042/u2-b.pdf","filename":"b.pdf","size":2,"content_type":"application/pdf"}]`)},
	}
}

func TestAttachmentURLClient(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	var gotInternal bool
	e.tickets.ListRepliesFn = func(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.TicketReply, error) {
		gotInternal = includeInternal
		return repliesWithAttachments(), nil
	}
	var presigned string
	var gotOpts ports.PresignOptions
	e.storage.PresignGetFn = func(ctx context.Context, key string, ttl time.Duration, opts ...ports.PresignOption) (string, error) {
		presigned = key
		assert.Equal(t, 15*time.Minute, ttl)
		for _, o := range opts {
			o(&gotOpts)
		}
		return "https://s3/" + key, nil
	}
	url, err := e.service().AttachmentURL(context.Background(), 7, 11, 1)
	require.NoError(t, err)
	assert.False(t, gotInternal, "client path must not see internal attachments")
	assert.Equal(t, "tickets/TKT-000042/u2-b.pdf", presigned)
	assert.Equal(t, "https://s3/tickets/TKT-000042/u2-b.pdf", url)
	// Download must always force a safe content type and an "attachment"
	// disposition, regardless of what content-type the object was stored
	// with (defense-in-depth against stored XSS).
	assert.Equal(t, "application/pdf", gotOpts.ResponseContentType)
	assert.Equal(t, `attachment; filename="b.pdf"`, gotOpts.ResponseContentDisposition)
}

func TestAttachmentURLStaffSeesInternal(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	var gotInternal bool
	e.tickets.ListRepliesFn = func(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.TicketReply, error) {
		gotInternal = includeInternal
		return repliesWithAttachments(), nil
	}
	_, err := e.service().AttachmentURL(context.Background(), 0, 11, 0)
	require.NoError(t, err)
	assert.True(t, gotInternal)
}

func TestAttachmentURLOutOfRange(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) { return openTicket(), nil }
	e.tickets.ListRepliesFn = func(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.TicketReply, error) {
		return repliesWithAttachments(), nil
	}
	_, err := e.service().AttachmentURL(context.Background(), 7, 11, 2)
	requireCode(t, err, apperr.CodeNotFound)
}

func TestAttachmentURLOwnership(t *testing.T) {
	e := newFixture()
	e.tickets.GetByIDFn = func(ctx context.Context, id int64) (*domain.Ticket, error) {
		tk := openTicket()
		tk.ClientID = ptr(int64(99))
		return tk, nil
	}
	_, err := e.service().AttachmentURL(context.Background(), 7, 11, 0)
	requireCode(t, err, apperr.CodeNotFound)
}

// Departments

func TestDepartmentCRUD(t *testing.T) {
	e := newFixture()
	e.tickets.CreateDepartmentFn = func(ctx context.Context, d *domain.TicketDepartment) error {
		d.ID = 8
		return nil
	}
	svc := e.service()
	dept, err := svc.CreateDepartment(context.Background(), 1, tickets.DepartmentInput{
		Name: "Billing", Email: "billing@example.com", Sort: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(8), dept.ID)
	assert.True(t, dept.Active, "active defaults to true")

	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return &domain.TicketDepartment{ID: 8, Name: "Billing", Active: true}, nil
	}
	inactive := false
	updated, err := svc.UpdateDepartment(context.Background(), 1, 8, tickets.DepartmentInput{
		Name: "Billing & Sales", Active: &inactive,
	})
	require.NoError(t, err)
	assert.Equal(t, "Billing & Sales", updated.Name)
	assert.False(t, updated.Active)

	require.NoError(t, svc.DeleteDepartment(context.Background(), 1, 8))
	require.Len(t, e.audit.Entries, 3)
	assert.Equal(t, "ticket_department.create", e.audit.Entries[0].Action)
	assert.Equal(t, "ticket_department.update", e.audit.Entries[1].Action)
	assert.Equal(t, "ticket_department.delete", e.audit.Entries[2].Action)
}

func TestDepartmentValidationAndErrors(t *testing.T) {
	e := newFixture()
	svc := e.service()

	_, err := svc.CreateDepartment(context.Background(), 1, tickets.DepartmentInput{Name: "x"})
	requireCode(t, err, apperr.CodeValidation)

	_, err = svc.CreateDepartment(context.Background(), 1, tickets.DepartmentInput{Name: "Sales", Email: "not-an-email"})
	requireCode(t, err, apperr.CodeValidation)

	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return nil, nil // simulate missing row
	}
	_, err = svc.UpdateDepartment(context.Background(), 1, 999, tickets.DepartmentInput{Name: "Sales"})
	requireCode(t, err, apperr.CodeNotFound)
	err = svc.DeleteDepartment(context.Background(), 1, 999)
	requireCode(t, err, apperr.CodeNotFound)

	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return &domain.TicketDepartment{ID: id, Name: "Support"}, nil
	}
	e.tickets.DeleteDepartmentFn = func(ctx context.Context, id int64) error {
		return apperr.Conflict("department has tickets and cannot be deleted")
	}
	err = svc.DeleteDepartment(context.Background(), 1, 3)
	requireCode(t, err, apperr.CodeConflict)
}

func TestListDepartmentsPassthrough(t *testing.T) {
	e := newFixture()
	e.tickets.ListDepartmentsFn = func(ctx context.Context, activeOnly bool) ([]domain.TicketDepartment, error) {
		assert.True(t, activeOnly)
		return []domain.TicketDepartment{*activeDept()}, nil
	}
	depts, err := e.service().ListDepartments(context.Background(), true)
	require.NoError(t, err)
	require.Len(t, depts, 1)
}

func TestCreateDepartmentActiveOverride(t *testing.T) {
	e := newFixture()
	var captured *domain.TicketDepartment
	e.tickets.CreateDepartmentFn = func(ctx context.Context, d *domain.TicketDepartment) error {
		captured = d
		return nil
	}
	inactive := false
	dept, err := e.service().CreateDepartment(context.Background(), 1, tickets.DepartmentInput{
		Name: "Billing", Active: &inactive,
	})
	require.NoError(t, err)
	require.NotNil(t, captured)
	assert.False(t, dept.Active, "explicit Active override must be honoured")
	assert.False(t, captured.Active)
}

// The client-supplied Content-Type header is never trusted (see
// TestCreateTicketAttachmentContentTypeIgnoresClientHeader for the spoofing
// regression), so the stored attachment type is derived solely from the
// filename extension.
func TestCreateTicketAttachmentContentTypeFallback(t *testing.T) {
	e := newFixture()
	e.tickets.GetDepartmentByIDFn = func(ctx context.Context, id int64) (*domain.TicketDepartment, error) {
		return activeDept(), nil
	}
	e.clients.GetByIDFn = func(ctx context.Context, id int64) (*domain.Client, error) { return testClient(), nil }
	e.tickets.NextNumberFn = func(ctx context.Context, scope string) (int64, error) { return 1, nil }
	var gotContentType string
	e.storage.PutFn = func(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
		gotContentType = contentType
		return nil
	}
	f := tickets.AttachmentUpload{Filename: "a.png", Size: 1, ContentType: "", Reader: strings.NewReader("x")}
	_, err := e.service().CreateTicket(context.Background(), 7, tickets.CreateTicketInput{
		DepartmentID: 3, Subject: "subject ok", Message: "message ok",
	}, []tickets.AttachmentUpload{f})
	require.NoError(t, err)
	assert.Equal(t, "image/png", gotContentType, "known extension must derive its real content type")
}
