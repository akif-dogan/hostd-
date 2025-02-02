package rhp

import (
	"container/list"
	"errors"
	"fmt"
	"sync"
	"time"

	rhp3 "go.sia.tech/core/rhp/v3"
)

type (
	// expiringPriceTable pairs a price table UID with an expiration timestamp.
	expiringPriceTable struct {
		uid    rhp3.SettingsID
		expiry time.Time
	}

	// A priceTableManager handles registered price tables and their expiration.
	priceTableManager struct {
		mu sync.RWMutex // protects the fields below

		// expirationList is a doubly linked list of price table UIDs. The list
		// will naturally be sorted by expiration time since validity is
		// constant and new price tables are appended to the list.
		expirationList *list.List
		// expirationTimer is a timer that fires when the next price table
		// expires. It is created using time.AfterFunc. It is set by the first
		// call to RegisterPriceTable and reset by pruneExpired.
		expirationTimer *time.Timer
		// priceTables is a map of valid price tables. The key is the UID of the
		// price table. Keys are removed by the loop in pruneExpired.
		priceTables map[rhp3.SettingsID]rhp3.HostPriceTable
	}
)

var (
	// ErrNoPriceTable is returned if a price table is requested but the UID
	// does not exist or has expired.
	ErrNoPriceTable = errors.New("no price table found")
)

// expirePriceTables removes expired price tables from the list of valid price
// tables. It is called by expirationTimer every time a price table expires.
func (pm *priceTableManager) pruneExpired() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	// loop through each price table and remove any that have expired
	for {
		ele := pm.expirationList.Front()
		if ele == nil {
			return
		}

		pt := ele.Value.(expiringPriceTable)
		// if the price table has not expired, reset the timer and return
		if rem := time.Until(pt.expiry); rem > 0 {
			// reset will cause pruneExpired to be called after the
			// remaining time.
			pm.expirationTimer.Reset(rem)
			return
		}
		// remove the uid from the list and the price table from the map
		pm.expirationList.Remove(ele)
		delete(pm.priceTables, pt.uid)
	}
}

// Get returns the price table with the given UID if it exists and
// has not expired.
func (pm *priceTableManager) Get(id [16]byte) (rhp3.HostPriceTable, error) {
	pm.mu.RLock()
	pt, ok := pm.priceTables[id]
	pm.mu.RUnlock()
	if !ok {
		return rhp3.HostPriceTable{}, ErrNoPriceTable
	}
	return pt, nil
}

// Register adds a price table to the list of valid price tables.
func (pm *priceTableManager) Register(pt rhp3.HostPriceTable) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	expiration := time.Now().Add(pt.Validity)
	pm.priceTables[pt.UID] = pt
	pm.expirationList.PushBack(expiringPriceTable{
		uid:    pt.UID,
		expiry: expiration,
	})
	if pm.expirationTimer == nil {
		// the expiration timer has not been set, set it now
		pm.expirationTimer = time.AfterFunc(time.Until(expiration), pm.pruneExpired)
	} else if len(pm.priceTables) == 1 {
		// if this is the only price table, reset the expiration timer. Reset()
		// will cause pruneExpired to be called after the remaining time. If
		// there are other price tables in the list, the timer should already be
		// set.
		pm.expirationTimer.Reset(time.Until(expiration))
	}
}

// readPriceTable reads the price table ID from the stream and returns an error
// if the price table is invalid or expired.
func (sh *SessionHandler) readPriceTable(s *rhp3.Stream) (rhp3.HostPriceTable, error) {
	// read the price table ID from the stream
	var uid rhp3.SettingsID
	if err := s.ReadRequest(&uid, 16); err != nil {
		return rhp3.HostPriceTable{}, fmt.Errorf("failed to read price table ID: %w", err)
	}
	return sh.priceTables.Get(uid)
}

// newPriceTableManager creates a new price table manager. It is safe for
// concurrent use.
func newPriceTableManager() *priceTableManager {
	pm := &priceTableManager{
		expirationList: list.New(),
		priceTables:    make(map[rhp3.SettingsID]rhp3.HostPriceTable),
	}
	return pm
}
