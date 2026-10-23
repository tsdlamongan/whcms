//go:build integration

// Integration tests against the real local Postgres (whmcs DB, migrations
// applied). They create their OWN fixture rows with uuid-suffixed
// identifiers and clean up after themselves - never touching shared or
// seeded data. Run: go test -tags integration ./internal/modules/tickets/
package tickets_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/tsdlamongan/whcms/backend/internal/domain"
	"github.com/tsdlamongan/whcms/backend/internal/modules/tickets"
	"github.com/tsdlamongan/whcms/backend/internal/platform/db"
	"github.com/tsdlamongan/whcms/backend/internal/ports"
	"github.com/tsdlamongan/whcms/backend/pkg/apperr"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable"
	}
	d, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Skipf("skipping integration test: cannot connect to postgres: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

// newDeptFixture inserts a ticket department and returns it.
func newDeptFixture(t *testing.T, d *db.DB, repo *tickets.Repo, tag string) *domain.TicketDepartment {
	t.Helper()
	ctx := context.Background()
	dept := &domain.TicketDepartment{Name: "IT Dept " + tag, Email: "dept-" + tag + "@example.com", Active: true, Sort: 99}
	require.NoError(t, repo.CreateDepartment(ctx, dept))
	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM ticket_departments WHERE id = $1`, dept.ID)
	})
	return dept
}

// newClientFixture inserts a user + client row and returns the client id.
func newClientFixture(t *testing.T, d *db.DB, tag string) int64 {
	t.Helper()
	ctx := context.Background()
	var userID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role) VALUES ($1, 'x', 'client')
		RETURNING id`, "tickets-it-"+tag+"@example.com").Scan(&userID))
	var clientID int64
	require.NoError(t, d.Querier(ctx).QueryRow(ctx, `
		INSERT INTO clients (user_id, first_name, last_name) VALUES ($1, 'IT', $2)
		RETURNING id`, userID, tag).Scan(&clientID))
	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM clients WHERE id = $1`, clientID)
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	return clientID
}

// newTicketFixture inserts a ticket via the repo (cleaned up via cascade on
// replies + explicit delete).
func newTicketFixture(t *testing.T, d *db.DB, repo *tickets.Repo, clientID, deptID int64, subject string) *domain.Ticket {
	t.Helper()
	ctx := context.Background()
	n, err := repo.NextNumber(ctx, domain.TicketCounterScope)
	require.NoError(t, err)
	tk := &domain.Ticket{
		TicketNumber: domain.FormatTicketNumber(n),
		ClientID:     &clientID,
		DepartmentID: deptID,
		Subject:      subject,
		Status:       domain.TicketOpen,
		Priority:     domain.PriorityMedium,
	}
	require.NoError(t, repo.Create(ctx, tk))
	t.Cleanup(func() {
		_, _ = d.Querier(ctx).Exec(ctx, `DELETE FROM tickets WHERE id = $1`, tk.ID)
	})
	return tk
}

func TestRepoTicketCRUD(t *testing.T) {
	d := testDB(t)
	repo := tickets.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()

	dept := newDeptFixture(t, d, repo, tag)
	clientID := newClientFixture(t, d, tag)
	tk := newTicketFixture(t, d, repo, clientID, dept.ID, "IT subject "+tag)
	require.NotZero(t, tk.ID)
	assert.Contains(t, tk.TicketNumber, "TKT-")

	got, err := repo.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	assert.Equal(t, tk.TicketNumber, got.TicketNumber)
	assert.Equal(t, domain.TicketOpen, got.Status)
	require.NotNil(t, got.ClientID)
	assert.Equal(t, clientID, *got.ClientID)

	// update
	got.Status = domain.TicketAnswered
	got.Priority = domain.PriorityHigh
	require.NoError(t, repo.Update(ctx, got))
	got2, err := repo.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TicketAnswered, got2.Status)
	assert.Equal(t, domain.PriorityHigh, got2.Priority)

	// not found
	_, err = repo.GetByID(ctx, -1)
	assert.ErrorIs(t, err, apperr.NotFound("ticket"))
	err = repo.Update(ctx, &domain.Ticket{ID: -1, DepartmentID: dept.ID, Status: domain.TicketOpen, Priority: domain.PriorityLow})
	assert.ErrorIs(t, err, apperr.NotFound("ticket"))
}

func TestRepoNextNumberIncrements(t *testing.T) {
	d := testDB(t)
	repo := tickets.NewRepo(d)
	ctx := context.Background()

	a, err := repo.NextNumber(ctx, domain.TicketCounterScope)
	require.NoError(t, err)
	b, err := repo.NextNumber(ctx, domain.TicketCounterScope)
	require.NoError(t, err)
	assert.Equal(t, a+1, b)
}

func TestRepoRepliesInternalFilter(t *testing.T) {
	d := testDB(t)
	repo := tickets.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()

	dept := newDeptFixture(t, d, repo, tag)
	clientID := newClientFixture(t, d, tag)
	tk := newTicketFixture(t, d, repo, clientID, dept.ID, "replies "+tag)

	attachments, _ := json.Marshal([]domain.TicketAttachment{
		{ObjectKey: "tickets/" + tk.TicketNumber + "/u-a.png", Filename: "a.png", Size: 3, ContentType: "image/png"},
	})
	require.NoError(t, repo.AddReply(ctx, &domain.TicketReply{
		TicketID: tk.ID, AuthorName: "Client", Message: "public msg", Attachments: attachments,
	}))
	require.NoError(t, repo.AddReply(ctx, &domain.TicketReply{
		TicketID: tk.ID, AuthorName: "Staff", Message: "internal msg", IsInternal: true,
	}))

	visible, err := repo.ListReplies(ctx, tk.ID, false)
	require.NoError(t, err)
	require.Len(t, visible, 1)
	assert.Equal(t, "public msg", visible[0].Message)
	var metas []domain.TicketAttachment
	require.NoError(t, json.Unmarshal(visible[0].Attachments, &metas))
	require.Len(t, metas, 1)
	assert.Equal(t, "a.png", metas[0].Filename)

	all, err := repo.ListReplies(ctx, tk.ID, true)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.True(t, all[1].IsInternal)
}

func TestRepoListsAndFilters(t *testing.T) {
	d := testDB(t)
	repo := tickets.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()

	dept := newDeptFixture(t, d, repo, tag)
	clientID := newClientFixture(t, d, tag)
	tk1 := newTicketFixture(t, d, repo, clientID, dept.ID, "alpha "+tag)
	tk2 := newTicketFixture(t, d, repo, clientID, dept.ID, "beta "+tag)
	tk2.Status = domain.TicketClosed
	tk2.Priority = domain.PriorityHigh
	require.NoError(t, repo.Update(ctx, tk2))

	// ListByClient with search narrows to our fixtures
	rows, total, err := repo.ListByClient(ctx, clientID, ports.ListParams{Search: tag})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 2)

	rows, total, err = repo.ListByClient(ctx, clientID, ports.ListParams{Search: tag, Status: "closed"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, tk2.ID, rows[0].ID)

	// generic List with search
	rows, total, err = repo.List(ctx, ports.ListParams{Search: "alpha " + tag})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, tk1.ID, rows[0].ID)

	// ListAdmin filters
	_, total, err = repo.ListAdmin(ctx, tickets.AdminListFilter{DepartmentID: dept.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)

	rows, total, err = repo.ListAdmin(ctx, tickets.AdminListFilter{DepartmentID: dept.ID, Status: "open"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, tk1.ID, rows[0].ID)

	// priority + client filters (the client-detail tab and the priority filter)
	rows, total, err = repo.ListAdmin(ctx, tickets.AdminListFilter{DepartmentID: dept.ID, Priority: "high"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, tk2.ID, rows[0].ID)

	_, total, err = repo.ListAdmin(ctx, tickets.AdminListFilter{ClientID: clientID})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "only this client's tickets")

	_, total, err = repo.ListAdmin(ctx, tickets.AdminListFilter{ClientID: clientID + 1_000_000})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "a client with no tickets matches nothing")

	rows, total, err = repo.ListAdmin(ctx, tickets.AdminListFilter{Search: tag, PerPage: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, rows, 1, "per_page honoured")
}

func TestRepoDepartments(t *testing.T) {
	d := testDB(t)
	repo := tickets.NewRepo(d)
	ctx := context.Background()
	tag := uuid.NewString()

	dept := newDeptFixture(t, d, repo, tag)

	got, err := repo.GetDepartmentByID(ctx, dept.ID)
	require.NoError(t, err)
	assert.Equal(t, dept.Name, got.Name)

	got.Active = false
	got.Name = "Renamed " + tag
	require.NoError(t, repo.UpdateDepartment(ctx, got))

	all, err := repo.ListDepartments(ctx, false)
	require.NoError(t, err)
	var foundAll bool
	for _, dd := range all {
		if dd.ID == dept.ID {
			foundAll = true
			assert.Equal(t, "Renamed "+tag, dd.Name)
		}
	}
	assert.True(t, foundAll)

	active, err := repo.ListDepartments(ctx, true)
	require.NoError(t, err)
	for _, dd := range active {
		assert.NotEqual(t, dept.ID, dd.ID, "inactive department must not appear in active list")
	}

	// delete blocked while a ticket references it
	clientID := newClientFixture(t, d, tag)
	tk := newTicketFixture(t, d, repo, clientID, dept.ID, "blocking "+tag)
	err = repo.DeleteDepartment(ctx, dept.ID)
	assert.ErrorIs(t, err, apperr.Conflict(""))

	_, err = d.Querier(ctx).Exec(ctx, `DELETE FROM tickets WHERE id = $1`, tk.ID)
	require.NoError(t, err)
	require.NoError(t, repo.DeleteDepartment(ctx, dept.ID))

	_, err = repo.GetDepartmentByID(ctx, dept.ID)
	assert.ErrorIs(t, err, apperr.NotFound("department"))
	err = repo.DeleteDepartment(ctx, dept.ID)
	assert.ErrorIs(t, err, apperr.NotFound("department"))
}
