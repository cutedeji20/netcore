package routertelemetry

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/netcore-isp/netcore/internal/database"
)

// Target is read from a protected worker file; credentials live in a separate file.
type Target struct {
	RouterID     string `json:"router_id"`
	URL          string `json:"url"`
	WANInterface string `json:"wan_interface"`
	Username     string `json:"username"`
	PasswordFile string `json:"password_file"`
	CACertFile   string `json:"ca_cert_file"`
}

type Collector struct {
	db      *database.Pool
	targets []Target
}

func NewCollector(db *database.Pool, configFile string) (*Collector, error) {
	if db == nil || configFile == "" {
		return nil, errors.New("router telemetry requires a database and configuration file")
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("read router telemetry configuration: %w", err)
	}
	var targets []Target
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, fmt.Errorf("decode router telemetry configuration: %w", err)
	}
	if len(targets) == 0 {
		return nil, errors.New("router telemetry needs at least one target")
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if err := validateTarget(target); err != nil {
			return nil, err
		}
		if seen[target.RouterID] {
			return nil, errors.New("router telemetry has a duplicate router id")
		}
		seen[target.RouterID] = true
	}
	return &Collector{db: db, targets: targets}, nil
}

func validateTarget(target Target) error {
	if _, err := uuid.Parse(target.RouterID); err != nil {
		return errors.New("router telemetry router_id must be a UUID")
	}
	u, err := url.Parse(target.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("router telemetry URL must be an HTTPS origin")
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsPrivate() {
		return errors.New("router telemetry URL must use a private IP address")
	}
	if target.WANInterface == "" || len(target.WANInterface) > 80 || target.Username == "" || target.PasswordFile == "" {
		return errors.New("router telemetry target is incomplete")
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return errors.New("router telemetry URL port is invalid")
		}
	}
	return nil
}

func (c *Collector) Poll(ctx context.Context, tenantID string) error {
	var failures []error
	for _, target := range c.targets {
		if err := c.pollTarget(ctx, tenantID, target); err != nil {
			failures = append(failures, fmt.Errorf("router %s: %w", target.RouterID, err))
		}
	}
	return errors.Join(failures...)
}

func (c *Collector) pollTarget(ctx context.Context, tenantID string, target Target) error {
	u, _ := url.Parse(target.URL)
	var managementIP string
	if err := c.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT management_ip::text FROM routers WHERE id=$1::uuid AND tenant_id=$2::uuid AND status <> 'RETIRED'`, target.RouterID, tenantID).Scan(&managementIP)
	}); err != nil {
		return fmt.Errorf("validate managed router: %w", err)
	}
	if !managementIPMatchesURL(managementIP, u.Hostname()) {
		return errors.New("telemetry URL does not match router management IP")
	}
	password, err := os.ReadFile(target.PasswordFile)
	if err != nil {
		return fmt.Errorf("read router telemetry password: %w", err)
	}
	password = bytes.TrimRight(password, "\r\n")
	if len(password) == 0 {
		return errors.New("router telemetry password is empty")
	}
	var roots *x509.CertPool
	if target.CACertFile != "" {
		roots, err = x509.SystemCertPool()
		if err != nil {
			return err
		}
		pem, err := os.ReadFile(target.CACertFile)
		if err != nil {
			return err
		}
		if !roots.AppendCertsFromPEM(pem) {
			return errors.New("router telemetry CA file contains no certificates")
		}
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var interfaces []map[string]string
	if err := readList(ctx, client, target, password, "/rest/interface/print", []string{"name", "rx-byte", "tx-byte"}, &interfaces); err != nil {
		return fmt.Errorf("read interface counters: %w", err)
	}
	var rx, txBytes int64
	found := false
	for _, item := range interfaces {
		if item["name"] != target.WANInterface {
			continue
		}
		rx, err = strconv.ParseInt(item["rx-byte"], 10, 64)
		if err != nil || rx < 0 {
			return errors.New("invalid WAN rx-byte counter")
		}
		txBytes, err = strconv.ParseInt(item["tx-byte"], 10, 64)
		if err != nil || txBytes < 0 {
			return errors.New("invalid WAN tx-byte counter")
		}
		found = true
		break
	}
	if !found {
		return errors.New("configured WAN interface was not found")
	}
	var hosts []json.RawMessage
	if err := readList(ctx, client, target, password, "/rest/ip/hotspot/host/print", []string{".id"}, &hosts); err != nil {
		return fmt.Errorf("read HotSpot hosts: %w", err)
	}
	return c.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO router_telemetry_samples (tenant_id,router_id,sampled_at,wan_interface,wan_rx_bytes,wan_tx_bytes,observed_hotspot_hosts) VALUES ($1::uuid,$2::uuid,date_trunc('minute',now()),$3,$4,$5,$6) ON CONFLICT DO NOTHING`, tenantID, target.RouterID, target.WANInterface, rx, txBytes, len(hosts))
		return err
	})
}

func managementIPMatchesURL(value, hostname string) bool {
	ip, err := netip.ParseAddr(value)
	if err != nil {
		prefix, prefixErr := netip.ParsePrefix(value)
		if prefixErr != nil {
			return false
		}
		ip = prefix.Addr()
	}
	return ip.String() == hostname
}

func readList(ctx context.Context, client *http.Client, target Target, password []byte, path string, props []string, result any) error {
	payload, _ := json.Marshal(map[string]any{".proplist": props})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(target.URL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(target.Username, string(password))
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("RouterOS returned HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result)
}
