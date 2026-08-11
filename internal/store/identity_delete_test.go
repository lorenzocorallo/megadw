package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSelectedAccountAndProxyCannotBeDeletedOutFromUnderAJob(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "megadw.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.Exec(`INSERT INTO mega_accounts(id,label,email,credential_ciphertext,status,created_at,updated_at) VALUES('account-1','Account','a@example.test',X'01','active',?,?)`, when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO proxy_profiles(id,name,type,host,port,timeout_seconds,enabled,created_at,updated_at) VALUES('proxy-1','Proxy','http','127.0.0.1',8080,15,1,?,?)`, when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO download_jobs(id,source_kind,source_handle,source_key_ciphertext,display_name,total_bytes,account_id,proxy_id,complete_root,incomplete_root,state,created_at,updated_at) VALUES('job-1','file','handle',X'01','file',0,'account-1','proxy-1','/complete','/incomplete','waiting_quota',?,?)`, when, when); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteMegaAccount(context.Background(), "account-1"); !errors.Is(err, ErrRecordInUse) {
		t.Fatalf("DeleteMegaAccount error = %v, want ErrRecordInUse", err)
	}
	if err := database.DeleteProxyProfile(context.Background(), "proxy-1"); !errors.Is(err, ErrRecordInUse) {
		t.Fatalf("DeleteProxyProfile error = %v, want ErrRecordInUse", err)
	}
	var accountID, proxyID string
	if err := database.QueryRow(`SELECT account_id, proxy_id FROM download_jobs WHERE id='job-1'`).Scan(&accountID, &proxyID); err != nil {
		t.Fatal(err)
	}
	if accountID != "account-1" || proxyID != "proxy-1" {
		t.Fatalf("job identity changed to account=%q proxy=%q", accountID, proxyID)
	}
}
