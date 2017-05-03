package backend

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	"github.com/digitalrebar/digitalrebar/go/common/store"
)

// TemplateInfo holds information on the templates in the boot
// environment that will be expanded into files.
//
// swagger:model
type TemplateInfo struct {
	// Name of the template
	//
	// required: true
	Name string
	// A text/template that specifies how to create
	// the final path the template should be
	// written to.
	//
	// required: true
	Path string
	// The ID of the template that should be expanded.  Either
	// this or Contents should be set
	//
	// required: false
	ID string
	// The contents that should be used when this template needs
	// to be expanded.  Either this or ID should be set.
	//
	// required: false
	Contents string
	pathTmpl *template.Template
}

func (ti *TemplateInfo) id() string {
	if ti.ID == "" {
		return ti.Name
	}
	return ti.ID
}

// OsInfo holds information about the operating system this BootEnv
// maps to.  Most of this information is optional for now.
//
// swagger:model
type OsInfo struct {
	// The name of the OS this BootEnv has.
	//
	// required: true
	Name string
	// The family of operating system (linux distro lineage, etc)
	Family string
	// The codename of the OS, if any.
	Codename string
	// The version of the OS, if any.
	Version string
	// The name of the ISO that the OS should install from.
	IsoFile string
	// The SHA256 of the ISO file.  Used to check for corrupt downloads.
	IsoSha256 string
	// The URL that the ISO can be downloaded from, if any.
	//
	// swagger:strfmt uri
	IsoUrl string
}

// BootEnv encapsulates the machine-agnostic information needed by the
// provisioner to set up a boot environment.
//
// swagger:model
type BootEnv struct {
	// The name of the boot environment.  Boot environments that install
	// an operating system must end in '-install'.
	//
	// required: true
	Name string
	// A description of this boot environment.  This should tell what
	// the boot environment is for, any special considerations that
	// shoudl be taken into account when using it, etc.
	Description string
	// The OS specific information for the boot environment.
	OS OsInfo
	// The templates that should be expanded into files for the
	// boot environment.
	//
	// required: true
	Templates []TemplateInfo
	// The partial path to the kernel for the boot environment.  This
	// should be path that the kernel is located at in the OS ISO or
	// install archive.
	//
	// required: true
	Kernel string
	// Partial paths to the initrds that should be loaded for the boot
	// environment. These should be paths that the initrds are located
	// at in the OS ISO or install archive.
	//
	// required: true
	Initrds []string
	// A template that will be expanded to create the full list of
	// boot parameters for the environment.
	//
	// required: true
	BootParams string
	// The list of extra required parameters for this
	// bootstate. They should be present as Machine.Params when
	// the bootenv is applied to the machine.
	//
	// required: true
	RequiredParams []string
	// The list of extra optional parameters for this
	// bootstate. They can be present as Machine.Params when
	// the bootenv is applied to the machine.  These are more
	// other consumers of the bootenv to know what parameters
	// could additionally be applied to the bootenv by the
	// renderer based upon the Machine.Params
	//
	OptionalParams []string
	// Whether the boot environment is useable.  This can only be set to
	// `true` if there are no Errors, and if there are any errors
	// Avaialble will be set to `false`, and will require user
	// intervention to set it back to `true`.
	//
	// required: true
	Available bool
	// Any errors that were recorded in the processing of this boot
	// environment.
	//
	// read only: true
	Errors []string
	// OnlyUnknown indicates whether this bootenv can be used without a
	// machine.  Only bootenvs with this flag set to `true` be used for
	// the unknownBootEnv preference.
	//
	// required: true
	OnlyUnknown    bool
	bootParamsTmpl *template.Template
	p              *DataTracker
	rootTemplate   *template.Template
	tmplMux        sync.Mutex
}

func bootEnvIndexes() []*Index {
	fix := AsBootEnv
	return []*Index{
		NewIndex("Name", func(i, j store.KeySaver) bool { return fix(i).Name < fix(j).Name }),
		NewIndex("Available", func(i, j store.KeySaver) bool { return !fix(i).Available && fix(j).Available }),
		NewIndex("OnlyUnknown", func(i, j store.KeySaver) bool { return !fix(i).OnlyUnknown && fix(j).OnlyUnknown }),
	}
}

func (b *BootEnv) Backend() store.SimpleStore {
	return b.p.getBackend(b)
}

func (b *BootEnv) pathFor(f string) string {
	res := b.OS.Name
	if strings.HasSuffix(b.Name, "-install") {
		res = path.Join(res, "install")
	}
	return path.Clean(path.Join(res, f))
}

func (b *BootEnv) localPathFor(f string) string {
	return path.Join(b.p.FileRoot, b.pathFor(f))
}

func (b *BootEnv) genRoot(commonRoot *template.Template, e *Error) *template.Template {
	var res *template.Template
	var err error
	if commonRoot == nil {
		res = template.New("")
	} else {
		res, err = commonRoot.Clone()
	}
	if err != nil {
		e.Errorf("Error cloning commonRoot: %v", err)
		return nil
	}
	buf := &bytes.Buffer{}
	for i := range b.Templates {
		ti := &b.Templates[i]
		if ti.Name == "" {
			e.Errorf("Templates[%d] has no Name", i)
			continue
		}
		if ti.Path == "" {
			e.Errorf("Templates[%d] has no Path", i)
			continue
		} else {
			pathTmpl, err := template.New(ti.Name).Parse(ti.Path)
			if err != nil {
				e.Errorf("Error compiling path template %s (%s): %v",
					ti.Name,
					ti.Path,
					err)
				continue
			} else {
				ti.pathTmpl = pathTmpl.Option("missingkey=error")
			}
		}
		if ti.ID != "" {
			if res.Lookup(ti.ID) == nil {
				e.Errorf("Templates[%d]: No common template for %s", i, ti.ID)
			}
			continue
		}
		if ti.Contents == "" {
			e.Errorf("Templates[%d] has both an empty ID and contents", i)
		}
		fmt.Fprintf(buf, `{{define "%s"}}%s{{end}}\n`, ti.Name, ti.Contents)
	}
	_, err = res.Parse(buf.String())
	if err != nil {
		e.Errorf("Error parsing inline templates: %v", err)
		return nil
	}
	if b.BootParams != "" {
		tmpl, err := template.New("machine").Parse(b.BootParams)
		if err != nil {
			e.Errorf("Error compiling boot parameter template: %v", err)
		} else {
			b.bootParamsTmpl = tmpl.Option("missingkey=error")
		}
	}
	if e.containsError {
		return nil
	}
	return res
}

func (b *BootEnv) OnLoad() error {
	e := &Error{o: b}
	b.tmplMux.Lock()
	defer b.tmplMux.Unlock()
	b.p.tmplMux.Lock()
	defer b.p.tmplMux.Unlock()
	b.rootTemplate = b.genRoot(b.p.rootTemplate, e)
	b.Errors = e.Messages
	b.Available = !e.containsError
	return nil
}

func (b *BootEnv) Prefix() string {
	return "bootenvs"
}

func (b *BootEnv) Key() string {
	return b.Name
}

func (b *BootEnv) New() store.KeySaver {
	return &BootEnv{Name: b.Name, p: b.p}
}

func (b *BootEnv) setDT(p *DataTracker) {
	b.p = p
}

func (b *BootEnv) explodeIso(e *Error) {
	// Only work on things that are requested.
	if b.OS.IsoFile == "" {
		b.p.Infof("debugBootEnv", "Explode ISO: Skipping %s becausing no iso image specified\n", b.Name)
		return
	}
	// Have we already exploded this?  If file exists, then good!
	canaryPath := b.localPathFor("." + b.OS.Name + ".rebar_canary")
	buf, err := ioutil.ReadFile(canaryPath)
	if err == nil && len(buf) != 0 && string(bytes.TrimSpace(buf)) == b.OS.IsoSha256 {
		b.p.Infof("debugBootEnv", "Explode ISO: canary file %s, in place and has proper SHA256\n", canaryPath)
		return
	}

	isoPath := filepath.Join(b.p.FileRoot, "isos", b.OS.IsoFile)
	if _, err := os.Stat(isoPath); os.IsNotExist(err) {
		e.Errorf("Explode ISO: iso doesn't exist: %s\n", isoPath)
		if b.OS.IsoUrl != "" {
			e.Errorf("You can download the required ISO from %s", b.OS.IsoUrl)
		}
		return
	}

	f, err := os.Open(isoPath)
	if err != nil {
		e.Errorf("Explode ISO: failed to open iso file %s: %v", isoPath, err)
		return
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		e.Errorf("Explode ISO: failed to read iso file %s: %v", isoPath, err)
		return
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	// This will wind up being saved along with the rest of the
	// hash because explodeIso is called by OnChange before the struct gets saved.
	if b.OS.IsoSha256 == "" {
		b.OS.IsoSha256 = hash
	}

	if hash != b.OS.IsoSha256 {
		e.Errorf("Explode ISO: SHA256 bad. actual: %v expected: %v", hash, b.OS.IsoSha256)
		return
	}

	// Call extract script
	// /explode_iso.sh b.OS.Name fileRoot isoPath path.Dir(canaryPath)
	cmdName := path.Join(b.p.FileRoot, "explode_iso.sh")
	cmdArgs := []string{b.OS.Name, b.p.FileRoot, isoPath, b.localPathFor(""), b.OS.IsoSha256}
	if out, err := exec.Command(cmdName, cmdArgs...).Output(); err != nil {
		e.Errorf("Explode ISO: explode_iso.sh failed for %s: %s", b.Name, err)
		e.Errorf("Command output:\n%s", string(out))

	} else {
		b.p.Infof("debugBootEnv", "Explode ISO: %s exploded to %s", b.OS.IsoFile, isoPath)
		b.p.Debugf("debugBootEnv", "Explode ISO Log:\n%s", string(out))
	}
	return
}

func (b *BootEnv) BeforeSave() error {
	e := &Error{Code: 422, Type: ValidationError, o: b}
	// If our basic templates do not parse, it is game over for us
	b.p.tmplMux.Lock()
	b.tmplMux.Lock()
	root := b.genRoot(b.p.rootTemplate, e)
	b.p.tmplMux.Unlock()
	if root == nil {
		b.tmplMux.Unlock()
		return e
	}
	b.rootTemplate = root
	b.tmplMux.Unlock()
	// Otherwise, we will save the BootEnv, but record
	// the list of errors and mark it as not available.
	//
	// First, we have to have an iPXE template, or a PXELinux and eLILO template, or all three.
	seenPxeLinux := false
	seenELilo := false
	seenIPXE := false
	for _, template := range b.Templates {
		if template.Name == "pxelinux" {
			seenPxeLinux = true
		}
		if template.Name == "elilo" {
			seenELilo = true
		}
		if template.Name == "ipxe" {
			seenIPXE = true
		}
	}
	if !seenIPXE {
		if !(seenPxeLinux && seenELilo) {
			e.Errorf("bootenv: Missing elilo or pxelinux template")
		}
	}
	// Make sure the ISO for this bootenv has been exploded locally so that
	// the boot env can use its contents.
	if b.OS.IsoFile != "" {
		b.explodeIso(e)
	}
	// If we have a non-empty Kernel, make sure it points at something kernel-ish.
	if b.Kernel != "" {
		kPath := b.localPathFor(b.Kernel)
		kernelStat, err := os.Stat(kPath)
		if err != nil {
			e.Errorf("bootenv: %s: missing kernel %s (%s)",
				b.Name,
				b.Kernel,
				kPath)
		} else if !kernelStat.Mode().IsRegular() {
			e.Errorf("bootenv: %s: invalid kernel %s (%s)",
				b.Name,
				b.Kernel,
				kPath)
		}
	}
	// Ditto for all the initrds.
	if len(b.Initrds) > 0 {
		for _, initrd := range b.Initrds {
			iPath := b.localPathFor(initrd)
			initrdStat, err := os.Stat(iPath)
			if err != nil {
				e.Errorf("bootenv: %s: missing initrd %s (%s)",
					b.Name,
					initrd,
					iPath)
				continue
			}
			if !initrdStat.Mode().IsRegular() {
				e.Errorf("bootenv: %s: invalid initrd %s (%s)",
					b.Name,
					initrd,
					iPath)
			}
		}
	}
	b.Errors = e.Messages
	b.Available = (len(b.Errors) == 0)

	return nil
}

func (b *BootEnv) BeforeDelete() error {
	e := &Error{Code: 409, Type: StillInUseError, o: b}
	var pref string
	var err error
	if b.OnlyUnknown {
		pref, err = b.p.Pref("unknownBootEnv")
		if err == nil && pref == b.Name {
			e.Errorf("BootEnv %s is the active unknownBootEnv, cannot remove it", pref)
		}
	} else {
		pref, err = b.p.Pref("defaultBootEnv")
		if err == nil && pref == b.Name {
			e.Errorf("BootEnv %s is the active defaultBootEnv, cannot remove it", pref)
		}
		machines := AsMachines(b.p.FetchAll(b.p.NewMachine()))
		for _, machine := range machines {
			if machine.BootEnv != b.Name {
				continue
			}
			e.Errorf("Bootenv %s in use by Machine %s", b.Name, machine.Name)
		}
	}
	return e.OrNil()
}

func (b *BootEnv) AfterDelete() {
	if b.OnlyUnknown {
		err := &Error{o: b}
		rts := b.Render(nil, err)
		if err.ContainsError() {
			b.Errors = err.Messages
		} else {
			rts.deregister(b.p.FS)
		}
	}
}

func (b *BootEnv) List() []*BootEnv {
	return AsBootEnvs(b.p.FetchAll(b))
}

func (p *DataTracker) NewBootEnv() *BootEnv {
	return &BootEnv{p: p}
}

func AsBootEnv(o store.KeySaver) *BootEnv {
	return o.(*BootEnv)
}

func AsBootEnvs(o []store.KeySaver) []*BootEnv {
	res := make([]*BootEnv, len(o))
	for i := range o {
		res[i] = AsBootEnv(o[i])
	}
	return res
}

func (b *BootEnv) Render(m *Machine, e *Error) renderers {
	var missingParams []string
	if len(b.RequiredParams) > 0 && m == nil {
		e.Errorf("Machine is nil or does not have params")
		return nil
	}
	r := newRenderData(b.p, m, b)
	for _, param := range b.RequiredParams {
		if !r.ParamExists(param) {
			missingParams = append(missingParams, param)
		}
	}
	if len(missingParams) > 0 {
		e.Errorf("missing required machine params for %s:\n %v", m.Name, missingParams)
	}
	rts := make(renderers, len(b.Templates))

	for i := range b.Templates {
		ti := &b.Templates[i]

		// first, render the path
		buf := &bytes.Buffer{}
		if err := ti.pathTmpl.Execute(buf, r); err != nil {
			e.Errorf("Error rendering template %s path %s: %v",
				ti.Name,
				ti.Path,
				err)
			continue
		}
		tmplPath := path.Clean("/" + buf.String())
		rts[i] = newRenderedTemplate(r, ti.id(), tmplPath)
	}
	return renderers(rts)
}

func (b *BootEnv) followUpSave() {
	if b.OnlyUnknown {
		err := &Error{o: b}
		rts := b.Render(nil, err)
		if err.ContainsError() {
			b.Errors = err.Messages
		} else {
			rts.register(b.p.FS)
		}
		return
	}
	machines := b.p.lockFor("machines")
	defer machines.Unlock()
	for i := range machines.d {
		machine := AsMachine(machines.d[i])
		if machine.BootEnv != b.Name {
			continue
		}
		err := &Error{o: b}
		rts := b.Render(machine, err)
		if err.ContainsError() {
			machine.Errors = err.Messages
		} else {
			rts.register(b.p.FS)
		}
	}
}
