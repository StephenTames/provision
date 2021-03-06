package backend

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"testing"

	"github.com/digitalrebar/store"
)

var (
	backingStore store.Store
	tmpDir       string
)

type crudTest struct {
	name string
	op   func(Stores, store.KeySaver, ObjectValidator) (bool, error)
	t    store.KeySaver
	pass bool
	ov   ObjectValidator
}

func (test *crudTest) Test(t *testing.T, d Stores) {
	passed, err := test.op(d, test.t, test.ov)
	msg := fmt.Sprintf("%s: wanted to pass: %v, passed: %v", test.name, test.pass, passed)
	if passed == test.pass {
		t.Log(msg)
		t.Logf("   err: %v", err)
	} else {
		t.Error(msg)
		t.Errorf("   err: %v", err)
	}
}

func loadExample(dt *DataTracker, kind, p string) (bool, error) {
	buf, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer buf.Close()
	d, unlocker := dt.LockEnts(kind)
	defer unlocker()
	var res store.KeySaver
	switch kind {
	case "users":
		res = dt.NewUser()
	case "machines":
		res = dt.NewMachine()
	case "profiles":
		res = dt.NewProfile()
	case "templates":
		res = dt.NewTemplate()
	case "bootenvs":
		res = dt.NewBootEnv()
	case "leases":
		res = dt.NewLease()
	case "reservations":
		res = dt.NewReservation()
	case "subnets":
		res = dt.NewSubnet()
	}

	dec := json.NewDecoder(buf)
	if err := dec.Decode(&res); err != nil {
		return false, err
	}
	return dt.Create(d, res, nil)
}

func mkDT(bs store.Store) *DataTracker {
	if bs == nil {
		bs, _ = store.Open("memory:///")
	}
	logger := log.New(os.Stdout, "dt", 0)
	dt := NewDataTracker(bs,
		tmpDir,
		tmpDir,
		"127.0.0.1",
		8091,
		8092,
		logger,
		map[string]string{"defaultBootEnv": "default", "unknownBootEnv": "ignore"},
		NewPublishers(logger))
	return dt
}

func TestMain(m *testing.M) {
	var err error
	tmpDir, err = ioutil.TempDir("", "datatracker-")
	if err != nil {
		log.Printf("Creating temp dir for file root failed: %v", err)
		os.Exit(1)
	}
	ret := m.Run()
	err = os.RemoveAll(tmpDir)
	if err != nil {
		log.Printf("Creating temp dir for file root failed: %v", err)
		os.Exit(1)
	}
	os.Exit(ret)
}
