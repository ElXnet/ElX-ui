package journal

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/mhsanaei/3x-ui/v3/internal/elxcontrol/session"
)

func TestReserveLastSlotConcurrentAcrossNodes(t *testing.T) {
	ctx, db := testDatabase(t)
	db.SetMaxOpenConns(12)
	entitlement := uuid.New()
	_, err := db.ExecContext(ctx, `INSERT INTO elx_entitlements(id,active_slots,valid_until)
		VALUES ($1,1,clock_timestamp()+interval '1 hour')`, entitlement)
	checkError(t, err, nil)
	nodes := [2]uuid.UUID{uuid.New(), uuid.New()}
	const attempts = 100
	requests := make([]session.ReservationRequest, attempts)
	for i := range requests {
		req := session.ReservationRequest{EntitlementID: entitlement, DeviceID: uuid.New(), NodeID: nodes[i%2],
			OperationID: uuid.New(), KeyGeneration: 1, ProofHash: [32]byte{1}}
		_, err = db.ExecContext(ctx, `INSERT INTO elx_devices(id,entitlement_id,key_generation) VALUES($1,$2,1)`,
			req.DeviceID, entitlement)
		checkError(t, err, nil)
		requests[i] = req
	}

	type result struct {
		req   session.ReservationRequest
		owner session.Owner
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, attempts)
	for _, req := range requests {
		go func() {
			<-start
			owner, err := session.Reserve(ctx, db, req)
			results <- result{req, owner, err}
		}()
	}
	close(start)
	var winner result
	successes := 0
	for range attempts {
		r := <-results
		if r.err == nil {
			winner = r
			successes++
		} else if !errors.Is(r.err, session.ErrFull) {
			t.Errorf("unexpected reservation failure: %v", r.err)
		}
	}
	if successes != 1 {
		t.Fatalf("reserved %d owners for one slot", successes)
	}
	owner, err := session.Reserve(ctx, db, winner.req)
	checkError(t, err, nil)
	if owner != winner.owner || owner.Generation != 1 || owner.State != "reserving" {
		t.Fatalf("unexpected replay result: %+v", owner)
	}
	otherNode := winner.req
	if otherNode.NodeID == nodes[0] {
		otherNode.NodeID = nodes[1]
	} else {
		otherNode.NodeID = nodes[0]
	}
	_, err = session.Reserve(ctx, db, otherNode)
	checkError(t, err, session.ErrConflict)
	otherNode.OperationID = uuid.New()
	_, err = session.Reserve(ctx, db, otherNode)
	checkError(t, err, session.ErrOwned)

	// An uncertain owner still consumes the slot.
	_, err = db.ExecContext(ctx, `UPDATE elx_session_owners SET state='unknown' WHERE id=$1`, owner.ID)
	checkError(t, err, nil)
	_, err = session.Reserve(ctx, db, otherNode)
	checkError(t, err, session.ErrOwned)
	otherDevice := requests[0]
	if otherDevice.DeviceID == winner.req.DeviceID {
		otherDevice = requests[1]
	}
	_, err = session.Reserve(ctx, db, otherDevice)
	checkError(t, err, session.ErrFull)

	stale := winner.req
	stale.KeyGeneration++
	_, err = session.Reserve(ctx, db, stale)
	checkError(t, err, session.ErrDenied)

	_, err = db.ExecContext(ctx, `UPDATE elx_entitlements SET valid_until=clock_timestamp() WHERE id=$1`, entitlement)
	checkError(t, err, nil)
	_, err = session.Reserve(ctx, db, winner.req)
	checkError(t, err, session.ErrDenied)
}

func TestRejectEmptyReservationWithoutDatabase(t *testing.T) {
	_, err := session.Reserve(context.Background(), nil, session.ReservationRequest{})
	checkError(t, err, session.ErrInvalid)
}
