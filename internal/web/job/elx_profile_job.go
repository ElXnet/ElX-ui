package job

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"gorm.io/gorm"
)

type ElXProfileJob struct {
	mu             sync.Mutex
	clientService  service.ClientService
	inboundService service.InboundService
	xrayService    service.XrayService
}

func NewElXProfileJob() *ElXProfileJob { return &ElXProfileJob{} }

type elxProfileJob struct {
	JobID          string    `json:"jobId"`
	ClaimID        string    `json:"claimId"`
	Operation      string    `json:"operation"`
	ProfileID      string    `json:"profileId"`
	EntitlementID  string    `json:"entitlementId"`
	VLESSID        string    `json:"vlessId"`
	SubscriptionID string    `json:"subscriptionId"`
	ClientEmail    string    `json:"clientEmail"`
	ValidUntil     time.Time `json:"validUntil"`
}

func (j elxProfileJob) valid() bool {
	return j.JobID != "" && j.ClaimID != "" && j.ProfileID != "" &&
		j.EntitlementID != "" && j.VLESSID != "" && j.SubscriptionID != "" &&
		strings.HasPrefix(j.ClientEmail, "elx-") && !j.ValidUntil.IsZero() &&
		(j.Operation == "create-disabled" || j.Operation == "enable" || j.Operation == "renew-expiry")
}

func elxProvisionInbounds(raw string) ([]int, error) {
	if raw == "" {
		return nil, errors.New("ELX_PROVISION_INBOUNDS is required")
	}
	var out []int
	seen := map[int]bool{}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 || n > 10000000 || seen[n] || len(out) >= 20 {
			return nil, errors.New("invalid or duplicate ELX_PROVISION_INBOUNDS")
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("no provisioning inbounds configured")
	}
	return out, nil
}

type elxLocalBridge struct{ client *http.Client }

func newElXLocalBridge(socket string) (*elxLocalBridge, error) {
	if !strings.HasPrefix(socket, "/") || socket == "/" {
		return nil, errors.New("ELX_MANAGEMENT_SOCKET must be absolute")
	}
	t := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	return &elxLocalBridge{client: &http.Client{Timeout: 8 * time.Second, Transport: t}}, nil
}

func (b *elxLocalBridge) post(ctx context.Context, path string, request any, response any) (int, error) {
	var body io.Reader
	if request != nil {
		raw, err := json.Marshal(request)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://control.local"+path, body)
	if err != nil {
		return 0, err
	}
	if request != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	result, err := b.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer result.Body.Close()
	if result.StatusCode == http.StatusNoContent {
		return result.StatusCode, nil
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return result.StatusCode, fmt.Errorf("Control provisioning returned HTTP %d", result.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, 4097))
	if err != nil {
		return result.StatusCode, err
	}
	if len(raw) > 4096 {
		return result.StatusCode, errors.New("oversized provisioning response")
	}
	if response != nil && json.Unmarshal(raw, response) != nil {
		return result.StatusCode, errors.New("malformed provisioning response")
	}
	return result.StatusCode, nil
}

func (j *ElXProfileJob) Run() {
	if !j.mu.TryLock() {
		return
	}
	defer j.mu.Unlock()
	socket := os.Getenv("ELX_MANAGEMENT_SOCKET")
	if socket == "" {
		return
	}
	inbounds, err := elxProvisionInbounds(os.Getenv("ELX_PROVISION_INBOUNDS"))
	if err != nil {
		// Do not create a partial client or silently choose a random inbound.
		logger.Warning("ElX provisioning: ", err)
		return
	}
	b, err := newElXLocalBridge(socket)
	if err != nil {
		logger.Warning("ElX provisioning: ", err)
		return
	}
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		var task elxProfileJob
		status, err := b.post(ctx, "/provision/v1/claim", nil, &task)
		if err != nil {
			cancel()
			logger.Warning("ElX provisioning claim unavailable: ", err)
			return
		}
		if status == http.StatusNoContent {
			cancel()
			return
		}
		if !task.valid() {
			cancel()
			logger.Warning("ElX provisioning refused malformed Control job")
			return
		}
		if err = j.apply(&task, inbounds); err != nil {
			cancel()
			// Job remains leased and can be replayed after the lease expires.
			logger.Warning("ElX disabled-profile provisioning failed: ", err)
			return
		}
		var ack map[string]any
		_, err = b.post(ctx, "/provision/v1/ack", map[string]string{
			"jobId": task.JobID, "claimId": task.ClaimID, "operation": task.Operation,
		}, &ack)
		cancel()
		if err != nil {
			logger.Warning("ElX provisioning ACK unavailable (safe to replay): ", err)
			return
		}
	}
}

// paidNodeConverged blocks premature fulfillment when the remote node
// is offline, disabled or has unapplied configuration changes.
func paidNodeConverged(enabled bool, status string, dirty bool) bool {
	return enabled && status == "online" && !dirty
}

// paidProfileConverged prevents Control from treating one successful master
// database update as proof that every configured remote node has the profile.
// A remote inbound is considered applied only after the regular node sync
// reports it online and not dirty. The master runtime must also be running
// without a held-back configuration.
func (j *ElXProfileJob) paidProfileConverged(record *model.ClientRecord, inbounds []int) error {
	if record == nil || !record.Enable {
		return errors.New("paid VPN profile is not enabled")
	}
	attached, err := j.clientService.GetInboundIdsForRecord(record.Id)
	if err != nil {
		return err
	}
	set := make(map[int]bool, len(attached))
	for _, id := range attached {
		set[id] = true
	}
	for _, id := range inbounds {
		if !set[id] {
			return fmt.Errorf("paid VLESS profile missing from inbound %d", id)
		}
		inbound, err := j.inboundService.GetInbound(id)
		if err != nil {
			return err
		}
		if inbound.Protocol != model.VLESS {
			return errors.New("paid profile attached to a non-VLESS inbound")
		}
		if inbound.NodeID != nil {
			enabled, status, dirty, _, err := (&service.NodeService{}).NodeSyncState(*inbound.NodeID)
			if err != nil {
				return err
			}
			if !paidNodeConverged(enabled, status, dirty) {
				return fmt.Errorf("paid profile not yet reconciled on remote node for inbound %d", id)
			}
		}
	}
	if !j.xrayService.IsXrayRunning() || j.xrayService.GetHeldBackConfig() != "" {
		return errors.New("paid profile not yet applied to a running master ElXray")
	}
	return nil
}

func (j *ElXProfileJob) apply(task *elxProfileJob, inbounds []int) error {
	if task == nil || !task.valid() {
		return errors.New("invalid Control profile job")
	}
	expiry := task.ValidUntil.UnixMilli()
	if expiry <= time.Now().UnixMilli() {
		return errors.New("paid profile is already expired")
	}
	record, err := j.clientService.GetRecordByEmail(nil, task.ClientEmail)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		record = nil
		err = nil
	}
	if err != nil {
		return err
	}
	if record != nil &&
		(record.UUID != task.VLESSID || record.SubID != task.SubscriptionID) {
		return errors.New("existing panel credentials do not match Control")
	}
	if task.Operation == "create-disabled" {
		for _, id := range inbounds {
			ib, err := j.inboundService.GetInbound(id)
			if err != nil {
				return err
			}
			if ib.Protocol != model.VLESS {
				return errors.New("paid profile can be provisioned only on configured VLESS inbounds")
			}
		}
		if record != nil && record.Enable {
			return errors.New("Control requested a disabled profile but panel already enabled it")
		}
		needRestart, err := j.clientService.Create(&j.inboundService, &service.ClientCreatePayload{
			Client: model.Client{
				Email: task.ClientEmail, ID: task.VLESSID, SubID: task.SubscriptionID,
				Flow: "xtls-rprx-vision", Enable: false, ExpiryTime: expiry,
				TrafficReset: "never", TotalGB: 0, LimitIP: 0,
			},
			InboundIds: inbounds,
		})
		if needRestart {
			j.xrayService.SetToNeedRestart()
		}
		if err != nil {
			return err
		}
		current, err := j.clientService.GetRecordByEmail(nil, task.ClientEmail)
		if err != nil {
			return err
		}
		attached, err := j.clientService.GetInboundIdsForRecord(current.Id)
		if err != nil {
			return err
		}
		have := map[int]bool{}
		for _, id := range attached {
			have[id] = true
		}
		for _, id := range inbounds {
			if !have[id] {
				return errors.New("paid VLESS profile not attached to all targets")
			}
		}
		if current.Enable {
			return errors.New("disabled provision unexpectedly enabled client")
		}
		return nil
	}
	if record == nil {
		return errors.New("cannot update absent paid VPN profile")
	}
	newExpiry := max(record.ExpiryTime, expiry)
	// A database row is NOT proof that the running ElXray accepted the
	// updated VLESS configuration. After a panel restart (or a previously
	// requested restart), retry may ACK only when the current Xray process
	// is running and there is no held-back configuration.
	if task.Operation == "enable" && record.Enable {
		return j.paidProfileConverged(record, inbounds)
	}
	client := model.Client{
		ID: record.UUID, Email: record.Email, SubID: record.SubID,
		Flow: record.Flow, Security: record.Security, Enable: record.Enable,
		ExpiryTime: newExpiry, LimitIP: record.LimitIP, TotalGB: record.TotalGB,
		TgID: record.TgID, Group: record.Group, Comment: record.Comment,
		Reset: record.Reset, ResetDay: record.ResetDay, ResetWeekday: record.ResetWeekday,
		ResetMax: record.ResetMax, TrafficReset: record.TrafficReset,
		TrafficResetDay: record.TrafficResetDay, CreatedAt: record.CreatedAt,
		Password: record.Password, Auth: record.Auth, Secret: record.Secret,
	}
	if task.Operation == "enable" {
		client.Enable = true
	}
	needRestart, err := j.clientService.Update(&j.inboundService, record.Id, client, record.LimitHwid)
	if needRestart {
		j.xrayService.SetToNeedRestart()
	}
	if err != nil {
		return err
	}
	if task.Operation == "enable" {
		if needRestart {
			return errors.New("paid profile stored; waiting for ElXray restart")
		}
		// A remote node's dirty flag is cleared only after convergence.
		// Do not ACK the payment outbox while even one node is pending.
		updated, err := j.clientService.GetRecordByEmail(nil, task.ClientEmail)
		if err != nil {
			return err
		}
		return j.paidProfileConverged(updated, inbounds)
	}
	return nil
}
