// Package secret keeps connection passwords in the system's credential
// store, under Seeker's own service names. A password only recall saved is
// moved across the first time it is read.
//
// On macOS it goes through the security(1) tool, as recall does, and not
// through the Keychain API: an item the API creates trusts the creating
// build by its cdhash, which a build signed without a team ID changes at
// every rebuild, so macOS asks again after each one. An item security(1)
// creates trusts security(1) itself, which no rebuild changes, and so
// neither asks to read recall's items nor Seeker's own.
package secret

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
)

// Each service is the prefix and the connection id, account the username,
// as recall keeps its own (credentialStore.ts) under recallPrefix.
const (
	servicePrefix = "seeker.connection."
	recallPrefix  = "recall.connection."
)

// errSecItemNotFound is security's exit status for a missing item.
const errSecItemNotFound = 44

// store is one place passwords live, named by service and account.
type store interface {
	set(service, account, password string) error
	get(service, account string) (password string, ok bool, err error)
	delete(service, account string) error
}

var backend store = mygoStore{}

func init() {
	if runtime.GOOS == "darwin" {
		backend = securityTool{}
	}
}

// Save stores password; an empty one stores nothing, as in recall.
func Save(connectionID, username, password string) error {
	if password == "" {
		return nil
	}
	return backend.set(servicePrefix+connectionID, username, password)
}

// Load returns the saved password, ok false when there is none. Failing
// to find Seeker's own, it looks for recall's and moves what it finds:
// recall's item goes once Seeker's is written, so recall no longer has it.
func Load(connectionID, username string) (password string, ok bool, err error) {
	password, ok, err = backend.get(servicePrefix+connectionID, username)
	if ok || err != nil {
		return password, ok, err
	}
	password, ok, err = backend.get(recallPrefix+connectionID, username)
	if !ok || err != nil {
		return "", false, err
	}
	// Recall's item stays unless Seeker's was written; a failed move
	// costs only another read next time.
	if backend.set(servicePrefix+connectionID, username, password) == nil {
		_ = backend.delete(recallPrefix+connectionID, username)
	}
	return password, true, nil
}

// Remove forgets Seeker's item; one not saved is not an error.
func Remove(connectionID, username string) error {
	return backend.delete(servicePrefix+connectionID, username)
}

// securityTool is macOS's credential store, reached with an argument
// vector rather than a shell, so nothing in a password needs quoting.
type securityTool struct{}

func (securityTool) set(service, account, password string) error {
	_, err := runSecurity("add-generic-password", "-a", account, "-s", service, "-w", password, "-U")
	return err
}

func (securityTool) get(service, account string) (string, bool, error) {
	out, err := runSecurity("find-generic-password", "-a", account, "-s", service, "-w")
	var ex *exec.ExitError
	if errors.As(err, &ex) && ex.ExitCode() == errSecItemNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	// security ends the secret with a newline; only that one is not the
	// password's, which may carry spaces at either end.
	return strings.TrimSuffix(strings.TrimSuffix(out, "\n"), "\r"), true, nil
}

func (securityTool) delete(service, account string) error {
	_, err := runSecurity("delete-generic-password", "-a", account, "-s", service)
	var ex *exec.ExitError
	if errors.As(err, &ex) && ex.ExitCode() == errSecItemNotFound {
		return nil
	}
	return err
}

// runSecurity returns security's output. Its own messages are left out of
// the error: they can echo prompt text, and a status says enough.
func runSecurity(args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command("/usr/bin/security", args...)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("security %s: %w", args[0], err)
	}
	return out.String(), nil
}

// mygoStore is every other platform's store: mygo's own.
type mygoStore struct{}

func (mygoStore) set(service, account, password string) error {
	return mygo.Secrets.Service(service).Set(account, []byte(password))
}

func (mygoStore) get(service, account string) (string, bool, error) {
	data, err := mygo.Secrets.Service(service).Get(account)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

func (mygoStore) delete(service, account string) error {
	return mygo.Secrets.Service(service).Delete(account)
}
