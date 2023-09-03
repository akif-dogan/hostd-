package sqlite

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"go.sia.tech/core/types"
	"go.sia.tech/hostd/host/storage"
	"go.uber.org/zap/zaptest"
	"lukechampine.com/frand"
)

func TestTransactionRetry(t *testing.T) {
	t.Run("transaction retry", func(t *testing.T) {
		log := zaptest.NewLogger(t)
		db, err := OpenDatabase(filepath.Join(t.TempDir(), "test.db"), log)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		err = db.transaction(func(tx txn) error { return nil }) // start a new empty transaction, should succeed immediately
		if err != nil {
			t.Fatal(err)
		}

		ch := make(chan struct{}, 1) // channel to synchronize the transaction goroutine

		// start a transaction in a goroutine and hold it open for 5 seconds
		// this should allow for the next transaction to be retried a few times
		go func() {
			err := db.transaction(func(tx txn) error {
				_, err := tx.Exec(`UPDATE global_settings SET host_key=?`, `foo`) // upgrade the transaction to an exclusive lock;
				if err != nil {
					return err
				}
				ch <- struct{}{}
				time.Sleep(500 * time.Millisecond)
				return nil
			})
			if err != nil {
				panic(err)
			}
			ch <- struct{}{}
		}()

		<-ch // wait for the transaction to start

		err = db.transaction(func(tx txn) error {
			_, err = tx.Exec(`UPDATE global_settings SET host_key=?`, `bar`) // should fail and be retried
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		<-ch // wait for the transaction to finish
	})

	t.Run("transaction timeout", func(t *testing.T) {
		log := zaptest.NewLogger(t)
		db, err := OpenDatabase(filepath.Join(t.TempDir(), "test.db"), log)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		err = db.transaction(func(tx txn) error { return nil }) // start a new empty transaction, should succeed immediately
		if err != nil {
			t.Fatal(err)
		}

		ch := make(chan struct{}, 1) // channel to synchronize the transaction goroutine

		go func() {
			err := db.transaction(func(tx txn) error {
				_, err := tx.Exec(`UPDATE global_settings SET host_key=?`, `foo`) // upgrade the transaction to an exclusive lock;
				if err != nil {
					return err
				}
				ch <- struct{}{}
				time.Sleep(2 * time.Second)
				return nil
			})
			if err != nil {
				panic(err)
			}
			ch <- struct{}{}
		}()

		<-ch // wait for the transaction to start

		err = db.transaction(func(tx txn) error {
			_, err := tx.Exec(`UPDATE global_settings SET host_key=?`, `bar`) // should fail and be retried
			if err != nil {
				return err
			}
			return nil
		})

		// verify the returned error is the busy error
		var sqliteErr sqlite3.Error
		if !errors.As(err, &sqliteErr) || sqliteErr.Code != sqlite3.ErrBusy {
			t.Fatalf("expected busy error, got %v", err)
		}

		<-ch // wait for the transaction to finish
	})
}

func TestClearLockedSectors(t *testing.T) {
	const sectors = 100
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "test.db"), zaptest.NewLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	id, err := db.AddVolume("foo", false)
	if err != nil {
		t.Fatal(err)
	} else if err = db.GrowVolume(id, sectors); err != nil {
		t.Fatal(err)
	} else if err = db.SetAvailable(id, true); err != nil {
		t.Fatal(err)
	}

	checkConsistency := func(locked, temp int) error {
		// check that the sectors are locked
		var count int
		err = db.queryRow(`SELECT COUNT(*) FROM locked_volume_sectors`).Scan(&count)
		if err != nil {
			return fmt.Errorf("query locked sectors: %w", err)
		} else if locked != count {
			return fmt.Errorf("expected %v locked sectors, got %v", locked, count)
		}

		// check that the temp sectors are still there
		err = db.queryRow(`SELECT COUNT(*) FROM temp_storage_sector_roots`).Scan(&count)
		if err != nil {
			return fmt.Errorf("query temp sectors: %w", err)
		} else if temp != count {
			return fmt.Errorf("expected %v temp sectors, got %v", temp, count)
		}

		m, err := db.Metrics(time.Now())
		if err != nil {
			return fmt.Errorf("metrics: %w", err)
		} else if m.Storage.TempSectors != uint64(temp) {
			return fmt.Errorf("expected %v temp sector metrics, got %v", temp, m.Storage.TempSectors)
		}
		return nil
	}

	// write temp sectors to the database
	var roots []types.Hash256
	for i := 0; i < sectors; i++ {
		sectorRoot := frand.Entropy256()
		commit, err := db.StoreSector(sectorRoot, func(storage.SectorLocation, bool) error {
			return nil
		})
		if err != nil {
			t.Fatal("add sector", i, err)
		}

		err = db.AddTemporarySectors([]storage.TempSector{
			{Root: sectorRoot, Expiration: uint64(i + 1)},
		})
		if err != nil {
			t.Fatal("add temp sector", i, err)
		} else if err = commit(); err != nil {
			t.Fatal("commit sector", i, err)
		}
		roots = append(roots, sectorRoot)
	}

	// check that the sectors have been stored and unlocked
	if err = checkConsistency(0, sectors); err != nil {
		t.Fatal(err)
	}

	// clear the locked sectors
	if err = db.clearLockedSectors(); err != nil {
		t.Fatal(err)
	}

	// check that no sectors were cleared
	if err = checkConsistency(0, sectors); err != nil {
		t.Fatal(err)
	}

	// lock half the sectors
	locked, roots := roots[:sectors/2], roots[sectors/2:]
	for _, root := range locked {
		_, _, err = db.SectorLocation(root) // lock the sector, but don't release it
		if err != nil {
			t.Fatal(err)
		}
	}

	// expire the locked temp sectors
	if err = db.ExpireTempSectors(50); err != nil {
		t.Fatal(err)
	}

	// check that half the sectors were cleared
	if err = checkConsistency(len(locked), len(roots)); err != nil {
		t.Fatal(err)
	}

	// clear the locked sectors
	if err = db.clearLockedSectors(); err != nil {
		t.Fatal(err)
	}

	// check that half the sectors were cleared
	if err = checkConsistency(0, len(roots)); err != nil {
		t.Fatal(err)
	}

	// lock the remaining sectors
	for _, root := range roots {
		_, _, err = db.SectorLocation(root) // lock the sector, but don't release it
		if err != nil {
			t.Fatal(err)
		}
	}

	// expire the remaining temp sectors
	if err = db.ExpireTempSectors(100); err != nil {
		t.Fatal(err)
	}

	// check that the locked sectors were not cleared
	if err = checkConsistency(len(roots), 0); err != nil {
		t.Fatal(err)
	}

	// clear the locked sectors
	if err = db.clearLockedSectors(); err != nil {
		t.Fatal(err)
	}

	// check that all sectors were cleared
	if err = checkConsistency(0, 0); err != nil {
		t.Fatal(err)
	}
}
