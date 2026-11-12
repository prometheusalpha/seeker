// Package secret keeps connection passwords in the system's credential
// store, under recall's service names so passwords recall saved carry over.
// On macOS the first read of an item recall created asks the user to let
// Seeker read it.
package secret

import (
	"errors"

	"github.com/egoist/mygo"
)

// servicePrefix is recall's (credentialStore.ts): service is the prefix
// and the connection id, account the username.
const servicePrefix = "recall.connection."

func service(connectionID string) mygo.SecretsModule {
	return mygo.Secrets.Service(servicePrefix + connectionID)
}

// Save stores password; an empty one stores nothing, as in recall.
func Save(connectionID, username, password string) error {
	if password == "" {
		return nil
	}
	return service(connectionID).Set(username, []byte(password))
}

// Load returns the saved password, ok false when there is none.
func Load(connectionID, username string) (password string, ok bool, err error) {
	data, err := service(connectionID).Get(username)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// Remove forgets the password; one not saved is not an error.
func Remove(connectionID, username string) error {
	return service(connectionID).Delete(username)
}
