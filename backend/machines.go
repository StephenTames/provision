package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"path"
	"strings"

	"github.com/digitalrebar/provision/backend/index"
	"github.com/digitalrebar/store"
	"github.com/pborman/uuid"
)

// Machine represents a single bare-metal system that the provisioner
// should manage the boot environment for.
// swagger:model
type Machine struct {
	validate

	// The name of the machine.  THis must be unique across all
	// machines, and by convention it is the FQDN of the machine,
	// although nothing enforces that.
	//
	// required: true
	// swagger:strfmt hostname
	Name string
	// A description of this machine.  This can contain any reference
	// information for humans you want associated with the machine.
	Description string
	// The UUID of the machine.
	// This is auto-created at Create time, and cannot change afterwards.
	//
	// required: true
	// swagger:strfmt uuid
	Uuid uuid.UUID
	// The UUID of the job that is currently running on the machine.
	//
	// swagger:strfmt uuid
	CurrentJob uuid.UUID
	// The IPv4 address of the machine that should be used for PXE
	// purposes.  Note that this field does not directly tie into DHCP
	// leases or reservations -- the provisioner relies solely on this
	// address when determining what to render for a specific machine.
	//
	// swagger:strfmt ipv4
	Address net.IP
	// The boot environment that the machine should boot into.  This
	// must be the name of a boot environment present in the backend.
	// If this field is not present or blank, the global default bootenv
	// will be used instead.
	BootEnv string
	// If there are any errors in the rendering process, they will be
	// available here.
	// read only: true
	Errors []string
	// An array of profiles to apply to this machine in order when looking
	// for a parameter during rendering.
	Profiles []string
	//
	// The Machine specific Profile Data - only used for the map (name and other
	// fields not used
	Profile Profile
	// The tasks this machine has to run.
	Tasks []string
	// required: true
	CurrentTask int
	// Indicates if the machine can run jobs or not.  Failed jobs mark the machine
	// not runnable.
	//
	// required: true
	Runnable bool
	p        *DataTracker

	// used during AfterSave() and AfterRemove() to handle boot environment changes.
	oldBootEnv string
}

func (n *Machine) HasTask(s string) bool {
	for _, p := range n.Tasks {
		if p == s {
			return true
		}
	}
	return false
}

func (n *Machine) Indexes() map[string]index.Maker {
	fix := AsMachine
	return map[string]index.Maker{
		"Key": index.MakeKey(),
		"Uuid": index.Make(
			true,
			"UUID string",
			func(i, j store.KeySaver) bool { return fix(i).Uuid.String() < fix(j).Uuid.String() },
			func(ref store.KeySaver) (gte, gt index.Test) {
				refUuid := fix(ref).Uuid.String()
				return func(s store.KeySaver) bool {
						return fix(s).Uuid.String() >= refUuid
					},
					func(s store.KeySaver) bool {
						return fix(s).Uuid.String() > refUuid
					}
			},
			func(s string) (store.KeySaver, error) {
				id := uuid.Parse(s)
				if id == nil {
					return nil, fmt.Errorf("Invalid UUID: %s", s)
				}
				return &Machine{Uuid: id}, nil
			}),
		"Name": index.Make(
			true,
			"string",
			func(i, j store.KeySaver) bool { return fix(i).Name < fix(j).Name },
			func(ref store.KeySaver) (gte, gt index.Test) {
				refName := fix(ref).Name
				return func(s store.KeySaver) bool {
						return fix(s).Name >= refName
					},
					func(s store.KeySaver) bool {
						return fix(s).Name > refName
					}
			},
			func(s string) (store.KeySaver, error) {
				return &Machine{Name: s}, nil
			}),
		"BootEnv": index.Make(
			false,
			"string",
			func(i, j store.KeySaver) bool { return fix(i).BootEnv < fix(j).BootEnv },
			func(ref store.KeySaver) (gte, gt index.Test) {
				refBootEnv := fix(ref).BootEnv
				return func(s store.KeySaver) bool {
						return fix(s).BootEnv >= refBootEnv
					},
					func(s store.KeySaver) bool {
						return fix(s).BootEnv > refBootEnv
					}
			},
			func(s string) (store.KeySaver, error) {
				return &Machine{BootEnv: s}, nil
			}),
		"Address": index.Make(
			false,
			"IP Address",
			func(i, j store.KeySaver) bool {
				n, o := big.Int{}, big.Int{}
				n.SetBytes(fix(i).Address.To16())
				o.SetBytes(fix(j).Address.To16())
				return n.Cmp(&o) == -1
			},
			func(ref store.KeySaver) (gte, gt index.Test) {
				addr := &big.Int{}
				addr.SetBytes(fix(ref).Address.To16())
				return func(s store.KeySaver) bool {
						o := big.Int{}
						o.SetBytes(fix(s).Address.To16())
						return o.Cmp(addr) != -1
					},
					func(s store.KeySaver) bool {
						o := big.Int{}
						o.SetBytes(fix(s).Address.To16())
						return o.Cmp(addr) == 1
					}
			},
			func(s string) (store.KeySaver, error) {
				addr := net.ParseIP(s)
				if addr == nil {
					return nil, fmt.Errorf("Invalid address: %s", s)
				}
				return &Machine{Address: addr}, nil
			}),
		"Runnable": index.Make(
			false,
			"boolean",
			func(i, j store.KeySaver) bool {
				return (!fix(i).Runnable) && fix(j).Runnable
			},
			func(ref store.KeySaver) (gte, gt index.Test) {
				avail := fix(ref).Runnable
				return func(s store.KeySaver) bool {
						v := fix(s).Runnable
						return v || (v == avail)
					},
					func(s store.KeySaver) bool {
						return fix(s).Runnable && !avail
					}
			},
			func(s string) (store.KeySaver, error) {
				res := &Machine{}
				switch s {
				case "true":
					res.Runnable = true
				case "false":
					res.Runnable = false
				default:
					return nil, errors.New("Runnable must be true or false")
				}
				return res, nil
			}),
	}
}

func (n *Machine) ParameterMaker(d Stores, parameter string) (index.Maker, error) {
	fix := AsMachine
	pobj := d("params").Find(parameter)
	if pobj == nil {
		return index.Maker{}, fmt.Errorf("Parameter %s must be defined", parameter)
	}
	param := AsParam(pobj)

	return index.Make(
		false,
		"parameter",
		func(i, j store.KeySaver) bool {
			ip, _ := fix(i).GetParam(d, parameter, true)
			jp, _ := fix(j).GetParam(d, parameter, true)

			// If both are nil, the Less is i < j == false
			if ip == nil && jp == nil {
				return false
			}
			// If ip is nil, the Less is i < j == true
			if ip == nil {
				if _, ok := jp.(bool); ok {
					return jp.(bool)
				}
				return true
			}
			// If jp is nil, the Less is i < j == false
			if jp == nil {
				return false
			}

			if _, ok := ip.(string); ok {
				return ip.(string) < jp.(string)
			}
			if _, ok := ip.(bool); ok {
				return jp.(bool) && !ip.(bool)
			}
			if _, ok := ip.(int); ok {
				return ip.(int) < jp.(int)
			}

			return false
		},
		func(ref store.KeySaver) (gte, gt index.Test) {
			jp, _ := fix(ref).GetParam(d, parameter, true)
			return func(s store.KeySaver) bool {
					ip, _ := fix(s).GetParam(d, parameter, true)

					// If both are nil, the Less is i >= j == true
					if ip == nil && jp == nil {
						return true
					}
					// If ip is nil, the Less is i >= j == false
					if ip == nil {
						if _, ok := jp.(bool); ok {
							return !jp.(bool)
						}
						return false
					}
					// If jp is nil, the Less is i >= j == true
					if jp == nil {
						return true
					}

					if _, ok := ip.(string); ok {
						return ip.(string) >= jp.(string)
					}
					if _, ok := ip.(bool); ok {
						return ip.(bool) || ip.(bool) == jp.(bool)
					}
					if _, ok := ip.(int); ok {
						return ip.(int) >= jp.(int)
					}
					return false
				},
				func(s store.KeySaver) bool {
					ip, _ := fix(s).GetParam(d, parameter, true)

					// If both are nil, the Less is i > j == false
					if ip == nil && jp == nil {
						return false
					}
					// If ip is nil, the Less is i > j == false
					if ip == nil {
						return false
					}
					// If jp is nil, the Less is i > j == true
					if jp == nil {
						if _, ok := ip.(bool); ok {
							return ip.(bool)
						}
						return true
					}

					if _, ok := ip.(string); ok {
						return ip.(string) > jp.(string)
					}
					if _, ok := ip.(bool); ok {
						return ip.(bool) && !jp.(bool)
					}
					if _, ok := ip.(int); ok {
						return ip.(int) > jp.(int)
					}
					return false
				}
		},
		func(s string) (store.KeySaver, error) {
			res := &Machine{Profile: Profile{Params: map[string]interface{}{}}}

			var obj interface{}
			err := json.Unmarshal([]byte(s), &obj)
			if err != nil {
				return nil, err
			}
			if err := param.ValidateValue(obj); err != nil {
				return nil, err
			}
			res.Profile.Params[parameter] = obj
			return res, nil
		}), nil

}

func (n *Machine) Backend() store.Store {
	return n.p.getBackend(n)
}

// HexAddress returns Address in raw hexadecimal format, suitable for
// pxelinux and elilo usage.
func (n *Machine) HexAddress() string {
	return Hexaddr(n.Address)
}

func (n *Machine) ShortName() string {
	idx := strings.Index(n.Name, ".")
	if idx == -1 {
		return n.Name
	}
	return n.Name[:idx]
}

func (n *Machine) UUID() string {
	return n.Uuid.String()
}

func (n *Machine) Prefix() string {
	return "machines"
}

func (n *Machine) Path() string {
	return path.Join(n.Prefix(), n.UUID())
}

func (n *Machine) Key() string {
	return n.UUID()
}

func (n *Machine) AuthKey() string {
	return n.Key()
}

func (n *Machine) HasProfile(name string) bool {
	for _, e := range n.Profiles {
		if e == name {
			return true
		}
	}
	return false
}

func (n *Machine) getProfile(d Stores, key string) *Profile {
	p := d("profiles").Find(key)
	if p != nil {
		return AsProfile(p)
	}
	return nil
}

func (n *Machine) GetParams() map[string]interface{} {
	m := n.Profile.Params
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

func (n *Machine) SetParams(d Stores, values map[string]interface{}) error {
	n.Profile.Params = values
	e := &Error{Code: 422, Type: ValidationError, o: n}
	_, e2 := n.p.Save(d, n, nil)
	e.Merge(e2)
	return e.OrNil()
}

func (n *Machine) GetParam(d Stores, key string, searchProfiles bool) (interface{}, bool) {
	mm := n.GetParams()
	if v, found := mm[key]; found {
		return v, true
	}
	if searchProfiles {
		for _, e := range n.Profiles {
			if p := n.getProfile(d, e); p != nil {
				if v, ok := p.GetParam(key, false); ok {
					return v, true
				}
			}
		}
		if gp := n.getProfile(d, n.p.GlobalProfileName); gp != nil {
			if v, ok := gp.Params[key]; ok {
				return v, true
			}
		}
	}
	return nil, false
}

func (n *Machine) New() store.KeySaver {
	res := &Machine{Name: n.Name, Uuid: n.Uuid, p: n.p, Tasks: []string{}, Profiles: []string{}}
	return store.KeySaver(res)
}

func (n *Machine) setDT(p *DataTracker) {
	n.p = p
}

func (n *Machine) OnCreate() error {
	// All machines start runnable.
	n.Runnable = true
	return nil
}

func (n *Machine) Validate() error {
	e := &Error{Code: 422, Type: ValidationError, o: n}
	if n.Uuid == nil {
		e.Errorf("Machine %#v was not assigned a uuid!", n)
	}
	if n.Name == "" {
		e.Errorf("Machine %s must have a name", n.Uuid)
	}
	if n.BootEnv == "" {
		n.BootEnv = n.p.defaultBootEnv
	}
	validateMaybeZeroIP4(e, n.Address)
	if err := index.CheckUnique(n, n.stores("machines").Items()); err != nil {
		e.Merge(err)
	}
	objs := n.stores
	tasks := objs("tasks")
	profiles := objs("profiles")
	bootenvs := objs("bootenvs")
	wantedProfiles := map[string]int{}
	for i, profileName := range n.Profiles {
		found := profiles.Find(profileName)
		if found == nil {
			e.Errorf("Profile %s (at %d) does not exist", profileName, i)
		} else {
			if alreadyAt, ok := wantedProfiles[profileName]; ok {
				e.Errorf("Duplicate profile %s: at %d and %d", profileName, alreadyAt, i)
			} else {
				wantedProfiles[profileName] = i
			}
		}
	}
	for i, taskName := range n.Tasks {
		if tasks.Find(taskName) == nil {
			e.Errorf("Task %s (at %d) does not exist", taskName, i)
		}
	}
	if nbFound := bootenvs.Find(n.BootEnv); nbFound == nil {
		e.Errorf("Bootenv %s does not exist", n.BootEnv)
	}
	return e.OrNil()
}

func (n *Machine) BeforeSave() error {
	if err := n.Validate(); err != nil {
		return err
	}

	e := &Error{Code: 422, Type: ValidationError, o: n}
	objs := n.stores
	bootenvs := objs("bootenvs")

	var env, oldEnv *BootEnv
	if nbFound := bootenvs.Find(n.BootEnv); nbFound == nil {
		e.Errorf("Bootenv %s does not exist", n.BootEnv)
		return e
	} else {
		env = AsBootEnv(nbFound)
	}
	if obFound := bootenvs.Find(n.oldBootEnv); obFound != nil {
		oldEnv = AsBootEnv(obFound)
	}
	if env.OnlyUnknown {
		e.Errorf("BootEnv %s does not allow Machine assignments, it has the OnlyUnknown flag.", env.Name)
	}
	if !env.Available {
		e.Errorf("Machine %s wants BootEnv %s, which is not available", n.UUID(), n.BootEnv)
	}
	if !e.ContainsError() {
		if oldEnv != nil {
			if oldEnv.Name != env.Name {
				oldEnv.Render(objs, n, e).deregister(n.p.FS)
				env.Render(objs, n, e).register(n.p.FS)
			}
		} else {
			env.Render(objs, n, e).register(n.p.FS)
		}
	}
	return e.OrNil()
}

func (n *Machine) OnChange(oldThing store.KeySaver) error {
	n.oldBootEnv = AsMachine(oldThing).BootEnv
	return nil
}

func (n *Machine) AfterSave() {

	// Have we changed bootenvs.  Rebuild the task lists
	if n.oldBootEnv != n.BootEnv {
		objs := n.stores
		profiles := objs("profiles")
		bootenvs := objs("bootenvs")

		// We get tasks by aggregating
		//   1. BootEnv tasks
		//   2. Profile tasks in order.
		//   3. Global Profile tasks (if they exist)

		taskList := []string{}

		env := AsBootEnv(bootenvs.Find(n.BootEnv))
		taskList = append(taskList, env.Tasks...)

		for _, pname := range n.Profiles {
			prof := AsProfile(profiles.Find(pname))
			taskList = append(taskList, prof.Tasks...)
		}

		gprof := AsProfile(profiles.Find(n.p.GlobalProfileName))
		taskList = append(taskList, gprof.Tasks...)

		// Reset the task list, set currentTask to 0
		n.Tasks = taskList
		n.CurrentTask = -1
		if len(taskList) == 0 {
			// Already done.
			n.CurrentTask = 0
		}

		// Reset this here to keep from looping forever.
		n.oldBootEnv = n.BootEnv

		_, e2 := n.p.Save(objs, n, nil)
		if e2 != nil {
			n.p.Logger.Printf("Failed to save machine in after Save. %v\n", n)
		}
	}
}

func (n *Machine) AfterDelete() {
	e := &Error{}
	if b := n.stores("bootenvs").Find(n.BootEnv); b != nil {
		AsBootEnv(b).Render(n.stores, n, e).deregister(n.p.FS)
	}
}

func (p *DataTracker) NewMachine() *Machine {
	return &Machine{p: p}
}

func AsMachine(o store.KeySaver) *Machine {
	return o.(*Machine)
}

func AsMachines(o []store.KeySaver) []*Machine {
	res := make([]*Machine, len(o))
	for i := range o {
		res[i] = AsMachine(o[i])
	}
	return res
}

var machineLockMap = map[string][]string{
	"get":     []string{"machines", "profiles", "params"},
	"create":  []string{"bootenvs", "machines", "tasks", "profiles", "templates", "params"},
	"update":  []string{"bootenvs", "machines", "tasks", "profiles", "templates", "params"},
	"patch":   []string{"bootenvs", "machines", "tasks", "profiles", "templates", "params"},
	"delete":  []string{"bootenvs", "machines"},
	"actions": []string{"machines", "profiles", "params"},
}

func (m *Machine) Locks(action string) []string {
	return machineLockMap[action]
}
