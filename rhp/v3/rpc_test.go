package rhp_test

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	rhpv2 "go.sia.tech/core/rhp/v2"
	rhpv3 "go.sia.tech/core/rhp/v3"
	"go.sia.tech/core/types"
	"go.sia.tech/hostd/host/settings"
	"go.sia.tech/hostd/internal/test"
	"go.uber.org/zap/zaptest"
	"lukechampine.com/frand"
)

func TestPriceTable(t *testing.T) {
	log := zaptest.NewLogger(t)
	renter, host, err := test.NewTestingPair(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer renter.Close()
	defer host.Close()

	pt, err := host.RHPv3PriceTable()
	if err != nil {
		t.Fatal(err)
	}

	session, err := renter.NewRHP3Session(context.Background(), host.RHPv3Addr(), host.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	retrieved, err := session.ScanPriceTable()
	if err != nil {
		t.Fatal(err)
	}
	// clear the UID field
	pt.UID = retrieved.UID
	if !reflect.DeepEqual(pt, retrieved) {
		t.Fatal("price tables don't match")
	}

	// pay for a price table using a contract payment
	revision, err := renter.FormContract(context.Background(), host.RHPv2Addr(), host.PublicKey(), types.Siacoins(10), types.Siacoins(20), 200)
	if err != nil {
		t.Fatal(err)
	}

	account := rhpv3.Account(renter.PublicKey())
	contractSession := session.WithContractPayment(&revision, renter.PrivateKey(), account)
	defer contractSession.Close()

	retrieved, err = contractSession.RegisterPriceTable()
	if err != nil {
		t.Fatal(err)
	}
	// clear the UID field
	pt.UID = retrieved.UID
	if !reflect.DeepEqual(pt, retrieved) {
		t.Fatal("price tables don't match")
	}

	// fund an account
	_, err = contractSession.FundAccount(account, types.Siacoins(1))
	if err != nil {
		t.Fatal(err)
	}

	// pay for a price table using an account
	retrieved, err = session.WithAccountPayment(account, renter.PrivateKey()).RegisterPriceTable()
	if err != nil {
		t.Fatal(err)
	}
	// clear the UID field
	pt.UID = retrieved.UID
	if !reflect.DeepEqual(pt, retrieved) {
		t.Fatal("price tables don't match")
	}
}

func TestAppendSector(t *testing.T) {
	log := zaptest.NewLogger(t)
	renter, host, err := test.NewTestingPair(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer renter.Close()
	defer host.Close()

	session, err := renter.NewRHP3Session(context.Background(), host.RHPv3Addr(), host.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	revision, err := renter.FormContract(context.Background(), host.RHPv2Addr(), host.PublicKey(), types.Siacoins(50), types.Siacoins(100), 200)
	if err != nil {
		t.Fatal(err)
	}

	// register the price table
	contractSession := session.WithContractPayment(&revision, renter.PrivateKey(), rhpv3.Account(renter.PublicKey()))
	pt, err := contractSession.RegisterPriceTable()
	if err != nil {
		t.Fatal(err)
	}

	// fund an account
	account := rhpv3.Account(renter.PublicKey())
	_, err = contractSession.FundAccount(account, types.Siacoins(10))
	if err != nil {
		t.Fatal(err)
	}

	// upload a sector
	accountSession := contractSession.WithAccountPayment(account, renter.PrivateKey())
	// calculate the cost of the upload
	cost, _ := pt.BaseCost().Add(pt.AppendSectorCost(revision.Revision.WindowEnd - renter.TipState().Index.Height)).Total()
	if cost.IsZero() {
		t.Fatal("cost is zero")
	}
	var sector [rhpv2.SectorSize]byte
	frand.Read(sector[:256])
	root := rhpv2.SectorRoot(&sector)
	err = accountSession.AppendSector(&sector, &revision, renter.PrivateKey(), cost)
	if err != nil {
		t.Fatal(err)
	}

	// download the sector
	cost, _ = pt.BaseCost().Add(pt.ReadSectorCost(rhpv2.SectorSize)).Total()
	downloaded, err := accountSession.ReadSector(root, 0, rhpv2.SectorSize, cost)
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(downloaded, sector[:]) {
		t.Fatal("downloaded sector doesn't match")
	}
}

func TestStoreSector(t *testing.T) {
	log := zaptest.NewLogger(t)
	renter, host, err := test.NewTestingPair(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer renter.Close()
	defer host.Close()

	session, err := renter.NewRHP3Session(context.Background(), host.RHPv3Addr(), host.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	revision, err := renter.FormContract(context.Background(), host.RHPv2Addr(), host.PublicKey(), types.Siacoins(50), types.Siacoins(100), 200)
	if err != nil {
		t.Fatal(err)
	}

	// register the price table
	contractSession := session.WithContractPayment(&revision, renter.PrivateKey(), rhpv3.Account(renter.PublicKey()))
	pt, err := contractSession.RegisterPriceTable()
	if err != nil {
		t.Fatal(err)
	}

	// fund an account
	account := rhpv3.Account(renter.PublicKey())
	_, err = contractSession.FundAccount(account, types.Siacoins(10))
	if err != nil {
		t.Fatal(err)
	}

	// upload a sector
	accountSession := contractSession.WithAccountPayment(account, renter.PrivateKey())
	// calculate the cost of the upload
	usage := pt.StoreSectorCost(10)
	cost, _ := usage.Total()
	var sector [rhpv2.SectorSize]byte
	frand.Read(sector[:256])
	root := rhpv2.SectorRoot(&sector)
	err = accountSession.StoreSector(&sector, 10, cost)
	if err != nil {
		t.Fatal(err)
	}

	// download the sector
	usage = pt.ReadSectorCost(rhpv2.SectorSize)
	cost, _ = usage.Total()
	downloaded, err := accountSession.ReadSector(root, 0, rhpv2.SectorSize, cost)
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(downloaded, sector[:]) {
		t.Fatal("downloaded sector doesn't match")
	}

	// mine until the sector expires
	if err := host.MineBlocks(types.VoidAddress, 10); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // sync time

	// prune the sectors
	if err := host.Storage().PruneSectors(); err != nil {
		t.Fatal(err)
	}

	// check that the sector was deleted
	usage = pt.ReadSectorCost(rhpv2.SectorSize)
	cost, _ = usage.Total()
	_, err = accountSession.ReadSector(root, 0, rhpv2.SectorSize, cost)
	if err == nil {
		t.Fatal("expected error when reading sector")
	}
}

func TestRenew(t *testing.T) {
	log := zaptest.NewLogger(t)
	renter, host, err := test.NewTestingPair(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer renter.Close()
	defer host.Close()

	t.Run("empty contract", func(t *testing.T) {
		state := renter.TipState()
		// form a contract
		origin, err := renter.FormContract(context.Background(), host.RHPv2Addr(), host.PublicKey(), types.Siacoins(10), types.Siacoins(20), state.Index.Height+200)
		if err != nil {
			t.Fatal(err)
		}

		settings, err := renter.Settings(context.Background(), host.RHPv2Addr(), host.PublicKey())
		if err != nil {
			t.Fatal(err)
		}

		// mine a few blocks into the contract
		if err := host.MineBlocks(host.WalletAddress(), 10); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)

		session, err := renter.NewRHP3Session(context.Background(), host.RHPv3Addr(), host.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()

		// register a price table to use for the renewal
		contractSess := session.WithContractPayment(&origin, renter.PrivateKey(), rhpv3.Account(renter.PublicKey()))
		pt, err := contractSess.RegisterPriceTable()
		if err != nil {
			t.Fatal(err)
		}

		state = renter.TipState()
		renewHeight := origin.Revision.WindowEnd + 10
		renterFunds := types.Siacoins(10)
		additionalCollateral := types.Siacoins(20)
		renewal, _, err := session.RenewContract(&origin, settings.Address, renter.PrivateKey(), renterFunds, additionalCollateral, renewHeight)
		if err != nil {
			t.Fatal(err)
		}

		// mine a block to confirm the revision
		if err := host.MineBlocks(host.WalletAddress(), 1); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)

		old, err := host.Contracts().Contract(origin.ID())
		if err != nil {
			t.Fatal(err)
		} else if old.Revision.Filesize != 0 {
			t.Fatal("filesize mismatch")
		} else if old.Revision.FileMerkleRoot != (types.Hash256{}) {
			t.Fatal("merkle root mismatch")
		} else if old.RenewedTo != renewal.ID() {
			t.Fatal("renewed to mismatch")
		} else if !old.Usage.RPCRevenue.Equals(pt.ContractPrice) {
			t.Fatalf("expected old contract rpc revenue to equal contract price %d, got %d", pt.ContractPrice, old.Usage.RPCRevenue)
		}

		contract, err := host.Contracts().Contract(renewal.ID())
		if err != nil {
			t.Fatal(err)
		} else if contract.Revision.Filesize != origin.Revision.Filesize {
			t.Fatal("filesize mismatch")
		} else if contract.Revision.FileMerkleRoot != origin.Revision.FileMerkleRoot {
			t.Fatal("merkle root mismatch")
		} else if !contract.LockedCollateral.Equals(additionalCollateral) {
			t.Fatalf("locked collateral mismatch: expected %d, got %d", additionalCollateral, contract.LockedCollateral)
		} else if !contract.Usage.RiskedCollateral.IsZero() {
			t.Fatalf("expected zero risked collateral, got %d", contract.Usage.RiskedCollateral)
		} else if !contract.Usage.RPCRevenue.Equals(pt.ContractPrice) {
			t.Fatalf("expected %d RPC revenue, got %d", settings.ContractPrice, contract.Usage.RPCRevenue)
		} else if !contract.Usage.StorageRevenue.Equals(pt.RenewContractCost) { // renew contract cost is treated as storage revenue because it is burned
			t.Fatalf("expected %d storage revenue, got %d", pt.RenewContractCost, contract.Usage.StorageRevenue)
		} else if contract.RenewedFrom != origin.ID() {
			t.Fatalf("expected renewed from %s, got %s", origin.ID(), contract.RenewedFrom)
		}
	})

	t.Run("non-empty contract", func(t *testing.T) {
		// form a contract
		state := renter.TipState()
		origin, err := renter.FormContract(context.Background(), host.RHPv2Addr(), host.PublicKey(), types.Siacoins(10), types.Siacoins(20), state.Index.Height+200)
		if err != nil {
			t.Fatal(err)
		}

		settings, err := renter.Settings(context.Background(), host.RHPv2Addr(), host.PublicKey())
		if err != nil {
			t.Fatal(err)
		}

		session, err := renter.NewRHP3Session(context.Background(), host.RHPv3Addr(), host.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()

		// register a price table to use for the renewal
		contractSess := session.WithContractPayment(&origin, renter.PrivateKey(), rhpv3.Account(renter.PublicKey()))
		pt, err := contractSess.RegisterPriceTable()
		if err != nil {
			t.Fatal(err)
		}

		// fund an account leaving no funds for the renewal
		accountID := rhpv3.Account(renter.PublicKey())
		if _, err := contractSess.FundAccount(accountID, origin.Revision.ValidRenterPayout().Sub(pt.FundAccountCost)); err != nil {
			t.Fatal(err)
		}

		// generate a sector
		var sector [rhpv2.SectorSize]byte
		frand.Read(sector[:256])

		// calculate the remaining duration of the contract
		var remainingDuration uint64
		contractExpiration := uint64(origin.Revision.WindowEnd)
		currentHeight := renter.TipState().Index.Height
		if contractExpiration < currentHeight {
			t.Fatal("contract expired")
		}

		accountSess := session.WithAccountPayment(accountID, renter.PrivateKey())

		// upload the sector
		remainingDuration = contractExpiration - currentHeight
		cost, _ := rhpv2.RPCAppendCost(settings, remainingDuration)
		if err := accountSess.AppendSector(&sector, &origin, renter.PrivateKey(), cost); err != nil {
			t.Fatal(err)
		}

		// mine a few blocks into the contract
		if err := host.MineBlocks(host.WalletAddress(), 10); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)

		state = renter.TipState()
		renewHeight := origin.Revision.WindowEnd + 10
		renterFunds := types.Siacoins(10)
		additionalCollateral := types.Siacoins(20)
		renewal, _, err := session.RenewContract(&origin, settings.Address, renter.PrivateKey(), renterFunds, additionalCollateral, renewHeight)
		if err != nil {
			t.Fatal(err)
		}

		extension := renewal.Revision.WindowEnd - origin.Revision.WindowEnd
		baseStorageRevenue := pt.RenewContractCost.Add(pt.WriteStoreCost.Mul64(origin.Revision.Filesize).Mul64(extension)) // renew contract cost is included because it is burned on failure
		baseRiskedCollateral := settings.Collateral.Mul64(extension).Mul64(origin.Revision.Filesize)

		expectedExchange := pt.ContractPrice.Add(pt.FundAccountCost)
		old, err := host.Contracts().Contract(origin.ID())
		if err != nil {
			t.Fatal(err)
		} else if old.Revision.Filesize != 0 {
			t.Fatal("filesize mismatch")
		} else if old.Revision.FileMerkleRoot != (types.Hash256{}) {
			t.Fatal("merkle root mismatch")
		} else if old.RenewedTo != renewal.ID() {
			t.Fatal("renewed to mismatch")
		} else if !old.Usage.RPCRevenue.Equals(expectedExchange) { // renewal renew goes on the new contract
			t.Fatalf("expected rpc revenue to equal contract price + fund account cost %d, got %d", expectedExchange, old.Usage.RPCRevenue)
		}

		contract, err := host.Contracts().Contract(renewal.ID())
		if err != nil {
			t.Fatal(err)
		} else if contract.Revision.Filesize != origin.Revision.Filesize {
			t.Fatal("filesize mismatch")
		} else if contract.Revision.FileMerkleRoot != origin.Revision.FileMerkleRoot {
			t.Fatal("merkle root mismatch")
		} else if contract.LockedCollateral.Cmp(additionalCollateral) <= 0 {
			t.Fatalf("locked collateral mismatch: expected at least %d, got %d", additionalCollateral, contract.LockedCollateral)
		} else if !contract.Usage.RPCRevenue.Equals(pt.ContractPrice) {
			t.Fatalf("expected %d RPC revenue, got %d", pt.ContractPrice, contract.Usage.RPCRevenue)
		} else if !contract.Usage.RiskedCollateral.Equals(baseRiskedCollateral) {
			t.Fatalf("expected %d risked collateral, got %d", baseRiskedCollateral, contract.Usage.RiskedCollateral)
		} else if !contract.Usage.StorageRevenue.Equals(baseStorageRevenue) {
			t.Fatalf("expected %d storage revenue, got %d", baseStorageRevenue, contract.Usage.StorageRevenue)
		} else if contract.RenewedFrom != origin.ID() {
			t.Fatalf("expected renewed from %s, got %s", origin.ID(), contract.RenewedFrom)
		}
	})
}

func BenchmarkAppendSector(b *testing.B) {
	log := zaptest.NewLogger(b)
	renter, host, err := test.NewTestingPair(b.TempDir(), log)
	if err != nil {
		b.Fatal(err)
	}
	defer renter.Close()
	defer host.Close()

	session, err := renter.NewRHP3Session(context.Background(), host.RHPv3Addr(), host.PublicKey())
	if err != nil {
		b.Fatal(err)
	}
	defer session.Close()

	revision, err := renter.FormContract(context.Background(), host.RHPv2Addr(), host.PublicKey(), types.Siacoins(50), types.Siacoins(100), 200)
	if err != nil {
		b.Fatal(err)
	}

	// register the price table
	contractSession := session.WithContractPayment(&revision, renter.PrivateKey(), rhpv3.Account(renter.PublicKey()))
	pt, err := contractSession.RegisterPriceTable()
	if err != nil {
		b.Fatal(err)
	}

	// fund an account
	account := rhpv3.Account(renter.PublicKey())
	_, err = contractSession.FundAccount(account, types.Siacoins(10))
	if err != nil {
		b.Fatal(err)
	}

	// upload a sector
	accountSession := contractSession.WithAccountPayment(account, renter.PrivateKey())
	// calculate the cost of the upload
	cost, _ := pt.BaseCost().Add(pt.AppendSectorCost(revision.Revision.WindowEnd - renter.TipState().Index.Height)).Total()
	if cost.IsZero() {
		b.Fatal("cost is zero")
	}

	var sectors [][rhpv2.SectorSize]byte
	for i := 0; i < b.N; i++ {
		var sector [rhpv2.SectorSize]byte
		frand.Read(sector[:256])
		sectors = append(sectors, sector)
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(rhpv2.SectorSize)

	for i := 0; i < b.N; i++ {
		err = accountSession.AppendSector(&sectors[i], &revision, renter.PrivateKey(), cost)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadSector(b *testing.B) {
	log := zaptest.NewLogger(b)
	renter, host, err := test.NewTestingPair(b.TempDir(), log)
	if err != nil {
		b.Fatal(err)
	}
	defer renter.Close()
	defer host.Close()

	s := settings.DefaultSettings
	s.MaxAccountBalance = types.Siacoins(100)
	s.MaxCollateral = types.Siacoins(10000)
	s.EgressPrice = types.ZeroCurrency
	s.IngressPrice = types.ZeroCurrency
	s.AcceptingContracts = true
	if err := host.UpdateSettings(s); err != nil {
		b.Fatal(err)
	}

	if err := host.AddVolume(filepath.Join(b.TempDir(), "data.dat"), uint64(b.N)); err != nil {
		b.Fatal(err)
	}

	session, err := renter.NewRHP3Session(context.Background(), host.RHPv3Addr(), host.PublicKey())
	if err != nil {
		b.Fatal(err)
	}
	defer session.Close()

	revision, err := renter.FormContract(context.Background(), host.RHPv2Addr(), host.PublicKey(), types.Siacoins(500), types.Siacoins(1000), 200)
	if err != nil {
		b.Fatal(err)
	}

	// register the price table
	contractSession := session.WithContractPayment(&revision, renter.PrivateKey(), rhpv3.Account(renter.PublicKey()))
	pt, err := contractSession.RegisterPriceTable()
	if err != nil {
		b.Fatal(err)
	}

	// fund an account
	account := rhpv3.Account(renter.PublicKey())
	_, err = contractSession.FundAccount(account, types.Siacoins(100))
	if err != nil {
		b.Fatal(err)
	}

	// upload a sector
	accountSession := contractSession.WithAccountPayment(account, renter.PrivateKey())
	// calculate the cost of the upload
	cost, _ := pt.BaseCost().Add(pt.AppendSectorCost(revision.Revision.WindowEnd - renter.TipState().Index.Height)).Total()
	if cost.IsZero() {
		b.Fatal("cost is zero")
	}

	var roots []types.Hash256
	for i := 0; i < b.N; i++ {
		var sector [rhpv2.SectorSize]byte
		frand.Read(sector[:256])

		err = accountSession.AppendSector(&sector, &revision, renter.PrivateKey(), cost)
		if err != nil {
			b.Fatal(err)
		}
		roots = append(roots, rhpv2.SectorRoot(&sector))
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(rhpv2.SectorSize)

	for i := 0; i < b.N; i++ {
		_, err = accountSession.ReadSector(roots[i], 0, rhpv2.SectorSize, cost)
		if err != nil {
			b.Fatal(err)
		}
	}
}
