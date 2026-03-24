package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	"path/filepath"
	"strings"
)

type Contact struct {
	Name   string `json:"name"`
	PeerID string `json:"peer_id"`
	IP     string `json:"ip"`
	Port   string `json:"port"`
}

type UserData struct {
	ID string `json:"ID"`
}

func (c Contact) Address() string {
	ip := strings.Trim(c.IP, "[]")
	return net.JoinHostPort(ip, c.Port)
}

func DeleteContact(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return fmt.Errorf("query is required")
	}

	contacts, err := ListContacts()
	if err != nil {
		return err
	}
	foundIndex := -1
	for i := range contacts {
		if contacts[i].PeerID == query || strings.EqualFold(contacts[i].Name, query) {
			foundIndex = i
			break
		}
	}

	if foundIndex == -1 {
		return fmt.Errorf("contact not found: %s", query)
	}
	contacts = append(contacts[:foundIndex], contacts[foundIndex+1:]...)
	return writeContacts(contacts)
}

func SaveUserData(id string) error {
	return SaveUserProfile(UserData{ID: id})
}

func SaveUserProfile(data UserData) error {
	data.ID = strings.TrimSpace(data.ID)
	if data.ID == "" {
		return fmt.Errorf("id is required")
	}

	path, err := UserDataFilePath()
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	return json.NewEncoder(file).Encode(data)
}

func GetUserData() (UserData, error) {
	path, err := UserDataFilePath()
	if err != nil {
		return UserData{}, err
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return UserData{}, nil // Файла нет — это не ошибка, просто профиль еще не создан
		}
		return UserData{}, err
	}
	defer file.Close()

	var data UserData
	err = json.NewDecoder(file).Decode(&data)
	if err != nil {
		return UserData{}, nil
	}
	data.ID = strings.TrimSpace(data.ID)
	return data, nil
}

func GetUserID() (string, error) {
	data, err := GetUserData()
	if err != nil {
		return "", err
	}
	return data.ID, nil
}
func RenameContact(query, newName string) error {
	query = strings.TrimSpace(query)
	newName = strings.TrimSpace(newName)
	if query == "" || newName == "" {
		return fmt.Errorf("query and new-name are required")
	}

	contacts, err := ListContacts()
	if err != nil {
		return err
	}

	foundIndex := -1
	for i := range contacts {
		if contacts[i].PeerID == query || strings.EqualFold(contacts[i].Name, query) {
			foundIndex = i
			break
		}
	}

	if foundIndex == -1 {
		return fmt.Errorf("contact not found: %s", query)
	}

	for i := range contacts {
		if i == foundIndex {
			continue
		}
		if strings.EqualFold(contacts[i].Name, newName) {
			return fmt.Errorf("contact name already exists: %s", newName)
		}
	}

	contacts[foundIndex].Name = newName
	return writeContacts(contacts)
}
func AddContact(c Contact) error {
	c.Name = strings.TrimSpace(c.Name)
	c.PeerID = strings.TrimSpace(c.PeerID)
	c.IP = strings.Trim(strings.TrimSpace(c.IP), "[]")
	c.Port = strings.TrimSpace(c.Port)
	if c.Name == "" || c.PeerID == "" || c.IP == "" || c.Port == "" {
		return fmt.Errorf("name, peer_id, ip and port are required")
	}
	if _, err := net.ResolveTCPAddr("tcp", c.Address()); err != nil {
		return fmt.Errorf("invalid contact address: %w", err)
	}

	contacts, err := ListContacts()
	if err != nil {
		return err
	}

	updated := false
	for i := range contacts {
		if contacts[i].PeerID == c.PeerID || strings.EqualFold(contacts[i].Name, c.Name) {
			contacts[i] = c
			updated = true
			break
		}
	}
	if !updated {
		contacts = append(contacts, c)
	}
	return writeContacts(contacts)
}

func ListContacts() ([]Contact, error) {
	path, err := ContactFilePath()
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Contact{}, nil
		}
		return nil, err
	}
	content := strings.TrimSpace(string(raw))
	if content == "" {
		return []Contact{}, nil
	}

	// Backward compatibility: older code could save contacts as JSON array.
	if strings.HasPrefix(content, "[") {
		var arr []Contact
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var contacts []Contact
	decoder := json.NewDecoder(file)
	for {
		var c Contact
		if err := decoder.Decode(&c); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, nil
}

func FindContact(query string) (Contact, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Contact{}, fmt.Errorf("query is required")
	}
	contacts, err := ListContacts()
	if err != nil {
		return Contact{}, err
	}
	for _, c := range contacts {
		if c.PeerID == query || strings.EqualFold(c.Name, query) {
			return c, nil
		}
	}
	return Contact{}, fmt.Errorf("contact not found: %s", query)
}

func writeContacts(contacts []Contact) error {
	path, err := ContactFilePath()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, c := range contacts {
		if err := encoder.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

func ContactFilePath() (string, error) {
	contactsDir := filepath.Join("data", "contacts")
	if err := os.MkdirAll(contactsDir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(contactsDir, "contacts.jsonl"), nil
}
func UserDataFilePath() (string, error) {
	contactsDir := filepath.Join("data", "UserData")
	if err := os.MkdirAll(contactsDir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(contactsDir, "UserData.jsonl"), nil
}
