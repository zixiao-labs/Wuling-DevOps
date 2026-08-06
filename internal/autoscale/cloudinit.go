package autoscale

import (
	"fmt"
	"strings"

	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// BuildUserData renders the VM startup script that self-configures the runner.
//
// It assumes the pool's image/template ships the runner binary and a systemd
// unit `wuling-runner.service` whose EnvironmentFile is
// /etc/wuling-runner/runner.env (documented in runner-config.example.yaml).
// The script writes that env file with the injected token + server URL, then
// starts the unit. The wlrt_ token is passed directly so the VM authenticates
// without a register round-trip — the autoscaler already owns the runner row.
func BuildUserData(serverURL, token string, pool Pool, tier TierSpec, runnerName string) string {
	labels := strings.Join(pool.Labels, ",")
	sizeGiB, hasDataDisk := pool.runnerDataDiskSizeGiB()
	hasDataDisk = hasDataDisk && sizeGiB > 0
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -e\n")
	if hasDataDisk {
		writeLinuxRunnerDataDiskSetup(&b, sizeGiB)
	}
	// The env file holds the runner's bearer token — keep it root-only. umask
	// guards the here-doc redirect; the explicit modes are belt-and-braces.
	b.WriteString("umask 077\n")
	b.WriteString("mkdir -p -m 0700 /etc/wuling-runner\n")
	b.WriteString("cat >/etc/wuling-runner/runner.env <<'WULING_EOF'\n")
	fmt.Fprintf(&b, "WULING_RUNNER_SERVER_URL=%s\n", serverURL)
	fmt.Fprintf(&b, "WULING_RUNNER_TOKEN=%s\n", token)
	fmt.Fprintf(&b, "WULING_RUNNER_NAME=%s\n", runnerName)
	fmt.Fprintf(&b, "WULING_RUNNER_LABELS=%s\n", labels)
	b.WriteString("WULING_RUNNER_CONCURRENCY=1\n")
	if hasDataDisk {
		// The runner forwards this non-secret bootstrap attestation only to
		// the internal self-check job. Its systemd mount condition makes a
		// successful runner process evidence that the configured work disk
		// was mounted before service startup.
		b.WriteString("WULING_RUNNER_DATA_DISK_READY=1\n")
		writeRunnerWorkspaceEnv(&b,
			"/var/lib/wuling-runner/work",
			"/var/lib/wuling-runner/tools",
			"/var/lib/wuling-runner/state",
		)
	}
	writeTierResourceLimits(&b, tier)
	b.WriteString("WULING_EOF\n")
	b.WriteString("chmod 600 /etc/wuling-runner/runner.env\n")
	b.WriteString("systemctl enable --now wuling-runner.service\n")
	return b.String()
}

// BuildUserDataForPool renders the startup script appropriate to the pool's OS
// *and* provider: Linux cloud-init/bash (BuildUserData) or Windows PowerShell
// (BuildWindowsUserData). An empty/unknown OS is treated as linux. macOS pools
// are rejected at config validation, so they never reach here.
func BuildUserDataForPool(serverURL, token string, pool Pool, tier TierSpec, runnerName string) string {
	if pool.OS == model.OSWindows {
		return BuildWindowsUserData(serverURL, token, pool, tier, runnerName)
	}
	return BuildUserData(serverURL, token, pool, tier, runnerName)
}

// windowsUserDataWrapper returns the prologue and epilogue a provider's Windows
// guest agent requires around a PowerShell payload. The two clouds disagree,
// and getting it wrong is silent: the agent simply does not recognise the blob
// as a script, the runner never starts, and the pool looks like it is failing
// to launch instances.
//
//   - AWS: EC2Launch v2 dispatches on XML-ish <powershell>…</powershell> tags.
//   - Alibaba Cloud: the Vminit agent's Plugin_Main_CloudinitUserData plugin
//     dispatches on a bare `[powershell]` marker that must be the very first
//     line with no leading whitespace, and takes NO closing marker.
//
// Anything else (a Proxmox/vCenter template running cloudbase-init, say) gets
// the AWS-style tags, which is what cloudbase-init also accepts.
func windowsUserDataWrapper(provider string) (prologue, epilogue string) {
	if provider == ProviderAliyun {
		return "[powershell]\n", ""
	}
	return "<powershell>\n", "</powershell>\n"
}

// BuildWindowsUserData renders the Windows VM startup script. Like the Linux
// variant, it assumes the image ships the runner binary and a Scheduled Task
// `wuling-runner` (running as SYSTEM) whose wrapper loads
// C:\ProgramData\wuling-runner\runner.env (documented in docs/pipelines.md
// §7.1). The script writes that env file with the injected token + server URL,
// then (re)starts the task. A Scheduled Task — not a service — because the
// runner is a plain console binary, so this needs no third-party service shim.
//
// C:\ProgramData is deliberate, and not merely conventional: Alibaba Cloud
// documents that user-data running at init time cannot write anywhere under
// C:\Users, because no user has logged in yet and the profile tree is not
// mounted. ProgramData is machine-scoped and always present.
func BuildWindowsUserData(serverURL, token string, pool Pool, tier TierSpec, runnerName string) string {
	labels := strings.Join(pool.Labels, ",")
	sizeGiB, hasDataDisk := pool.runnerDataDiskSizeGiB()
	hasDataDisk = hasDataDisk && sizeGiB > 0
	prologue, epilogue := windowsUserDataWrapper(pool.Provider)
	var b strings.Builder
	b.WriteString(prologue)
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	if hasDataDisk {
		writeWindowsRunnerDataDiskSetup(&b, sizeGiB)
	}
	b.WriteString("$dir = 'C:\\ProgramData\\wuling-runner'\n")
	b.WriteString("New-Item -ItemType Directory -Force -Path $dir | Out-Null\n")
	// runner.env uses the same KEY=VALUE lines the Linux systemd unit reads. A
	// single-quoted here-string is fully literal, so a token/URL containing `$`
	// or a backtick is never interpreted by PowerShell.
	b.WriteString("$runnerEnv = @'\n")
	fmt.Fprintf(&b, "WULING_RUNNER_SERVER_URL=%s\n", serverURL)
	fmt.Fprintf(&b, "WULING_RUNNER_TOKEN=%s\n", token)
	fmt.Fprintf(&b, "WULING_RUNNER_NAME=%s\n", runnerName)
	fmt.Fprintf(&b, "WULING_RUNNER_LABELS=%s\n", labels)
	b.WriteString("WULING_RUNNER_CONCURRENCY=1\n")
	if hasDataDisk {
		// The PowerShell setup runs before this environment file is written,
		// so this is an attestation that the non-system disk setup completed.
		b.WriteString("WULING_RUNNER_DATA_DISK_READY=1\n")
		writeRunnerWorkspaceEnv(&b,
			`W:\wuling-runner\work`,
			`W:\wuling-runner\tools`,
			`W:\wuling-runner\state`,
		)
	}
	writeTierResourceLimits(&b, tier)
	b.WriteString("'@\n")
	b.WriteString("Set-Content -Path \"$dir\\runner.env\" -Value $runnerEnv -Encoding ascii\n")
	// The env file holds the runner's bearer token — restrict it to Administrators
	// and SYSTEM (the Linux side does the equivalent with chmod 600 + root-only).
	b.WriteString("icacls \"$dir\\runner.env\" /inheritance:r /grant:r \"*S-1-5-32-544:F\" \"*S-1-5-18:F\" | Out-Null\n")
	// (Re)start the image-provided Scheduled Task now that runner.env is written.
	b.WriteString("& schtasks /End /TN 'wuling-runner' 2>$null | Out-Null\n")
	b.WriteString("& schtasks /Run /TN 'wuling-runner'\n")
	b.WriteString(epilogue)
	return b.String()
}

// writeLinuxRunnerDataDiskSetup emits a fail-closed setup sequence. Capacity is
// matched as whole GiB cloud volumes (AWS/Aliyun VolumeSize units), never by
// rounding a sub-GiB quantity up and hoping the guest reports that larger size.
// Disks already at /var/lib/wuling-runner (or unmounted with the matching
// ext4 fstab entry) are accepted without reformatting so user-data reruns
// can continue into runner.env setup.
func writeLinuxRunnerDataDiskSetup(b *strings.Builder, expectedGiB int) {
	expectedBytes := int64(expectedGiB) * (1 << 30)
	fmt.Fprintf(b, "wuling_runner_expected_data_disk_bytes=%d\n", expectedBytes)
	b.WriteString(`wuling_runner_disk_bytes() {
  lsblk -bdn -o SIZE "$1" 2>/dev/null | tr -d '[:space:]'
}

wuling_runner_is_capacity_match() {
  local disk="$1"
  [ "$(wuling_runner_disk_bytes "$disk")" = "$wuling_runner_expected_data_disk_bytes" ]
}

wuling_runner_is_raw_data_disk() {
  local disk="$1"
  [ "$(lsblk -dn -o TYPE "$disk" 2>/dev/null)" = "disk" ] || return 1
  [ "$(lsblk -nr -o TYPE "$disk" 2>/dev/null | wc -l | tr -d '[:space:]')" = "1" ] || return 1
  [ "$(lsblk -dn -o RO "$disk" 2>/dev/null | tr -d '[:space:]')" = "0" ] || return 1
  local mountpoint
  mountpoint="$(lsblk -nr -o MOUNTPOINT "$disk" 2>/dev/null | awk 'NF { print; exit }')"
  # Already mounted elsewhere cannot be the runner workspace disk.
  if [ -n "$mountpoint" ] && [ "$mountpoint" != "/var/lib/wuling-runner" ]; then
    return 1
  fi
  local fstype
  fstype="$(lsblk -dn -o FSTYPE "$disk" 2>/dev/null | tr -d '[:space:]')"
  if [ -n "$fstype" ] || [ -n "$mountpoint" ]; then
    # Formatted (and optionally already mounted at the workspace): require
    # ext4 + expected fstab so user-data reruns remount without mkfs.
    [ "$fstype" = "ext4" ] || return 1
    local uuid
    uuid="$(blkid -s UUID -o value "$disk" 2>/dev/null)"
    [ -n "$uuid" ] || return 1
    grep -Fqx "UUID=$uuid /var/lib/wuling-runner ext4 defaults 0 2" /etc/fstab
    return $?
  fi
  local signatures
  signatures="$(wipefs --noheadings --output TYPE "$disk" 2>/dev/null)" || return 1
  [ -z "$signatures" ]
}

wuling_runner_candidate_disks=()
while IFS= read -r disk; do
  if wuling_runner_is_capacity_match "$disk" && wuling_runner_is_raw_data_disk "$disk"; then
    wuling_runner_candidate_disks+=("$disk")
  fi
done < <(lsblk -dnpo NAME,TYPE | awk '$2 == "disk" { print $1 }')
if [ "${#wuling_runner_candidate_disks[@]}" -ne 1 ]; then
  echo "wuling-runner: expected exactly one non-boot data disk with configured capacity" >&2
  exit 1
fi
runner_data_disk="${wuling_runner_candidate_disks[0]}"
runner_data_disk_uuid="$(blkid -s UUID -o value "$runner_data_disk" 2>/dev/null || true)"
runner_data_disk_fstab=""
if [ -n "$runner_data_disk_uuid" ]; then
  runner_data_disk_fstab="UUID=$runner_data_disk_uuid /var/lib/wuling-runner ext4 defaults 0 2"
fi
if [ -n "$runner_data_disk_fstab" ] && grep -Fqx "$runner_data_disk_fstab" /etc/fstab; then
  mkdir -p /var/lib/wuling-runner
  if ! mountpoint -q /var/lib/wuling-runner; then
    mount "$runner_data_disk" /var/lib/wuling-runner
  fi
else
  if ! command -v mkfs.ext4 >/dev/null 2>&1; then
    echo "wuling-runner: mkfs.ext4 is required for runner_data_disk" >&2
    exit 1
  fi
  mkfs.ext4 -F "$runner_data_disk" >/dev/null
  runner_data_disk_uuid="$(blkid -s UUID -o value "$runner_data_disk")"
  if [ -z "$runner_data_disk_uuid" ]; then
    echo "wuling-runner: could not read runner_data_disk UUID" >&2
    exit 1
  fi
  runner_data_disk_fstab="UUID=$runner_data_disk_uuid /var/lib/wuling-runner ext4 defaults 0 2"
  if ! grep -Fqx "$runner_data_disk_fstab" /etc/fstab; then
    printf '%s\n' "$runner_data_disk_fstab" >>/etc/fstab
  fi
  mkdir -p /var/lib/wuling-runner
  mount "$runner_data_disk" /var/lib/wuling-runner
fi
if ! mountpoint -q /var/lib/wuling-runner; then
  echo "wuling-runner: runner_data_disk did not mount" >&2
  exit 1
fi
mkdir -p /var/lib/wuling-runner/work /var/lib/wuling-runner/tools /var/lib/wuling-runner/state
mkdir -p /etc/systemd/system/wuling-runner.service.d
cat >/etc/systemd/system/wuling-runner.service.d/data-disk.conf <<'WULING_MOUNT_EOF'
[Unit]
RequiresMountsFor=/var/lib/wuling-runner
ConditionPathIsMountPoint=/var/lib/wuling-runner
WULING_MOUNT_EOF
systemctl daemon-reload
`)
}

// writeWindowsRunnerDataDiskSetup emits the Windows equivalent of the Linux
// fail-closed setup: exactly one matching-capacity non-boot RAW disk may be
// initialized, and W: must not already point at a volume.
func writeWindowsRunnerDataDiskSetup(b *strings.Builder, expectedGiB int) {
	expectedBytes := int64(expectedGiB) * (1 << 30)
	fmt.Fprintf(b, "$wulingRunnerExpectedDataDiskBytes = %d\n", expectedBytes)
	b.WriteString(`$existingW = Get-Volume -DriveLetter W -ErrorAction SilentlyContinue
if ($null -ne $existingW) {
  throw 'wuling-runner requires W: to be unused for runner_data_disk'
}
$rawDisks = @(Get-Disk | Where-Object {
  $_.PartitionStyle -eq 'RAW' -and
  -not $_.IsBoot -and
  -not $_.IsSystem -and
  -not $_.IsReadOnly -and
  $_.Size -eq $wulingRunnerExpectedDataDiskBytes
})
if ($rawDisks.Count -ne 1) {
  throw 'wuling-runner requires exactly one raw, non-boot data disk with configured capacity'
}
$runnerDisk = $rawDisks[0]
if ($runnerDisk.IsBoot -or $runnerDisk.IsSystem) {
  throw 'wuling-runner refused a boot or system disk'
}
if ($runnerDisk.IsOffline) {
  Set-Disk -Number $runnerDisk.Number -IsOffline $false
}
$runnerDisk = Initialize-Disk -Number $runnerDisk.Number -PartitionStyle GPT -PassThru
$runnerPartition = New-Partition -DiskNumber $runnerDisk.Number -UseMaximumSize -DriveLetter W
Format-Volume -Partition $runnerPartition -FileSystem NTFS -NewFileSystemLabel 'wuling-runner' -Confirm:$false -Force | Out-Null
$runnerPartitionCheck = Get-Partition -DriveLetter W -ErrorAction Stop
if ($runnerPartitionCheck.DiskNumber -ne $runnerDisk.Number) {
  throw 'wuling-runner data disk mounted at an unexpected drive'
}
$runnerRoot = 'W:\wuling-runner'
New-Item -ItemType Directory -Force -Path $runnerRoot, "$runnerRoot\work", "$runnerRoot\tools", "$runnerRoot\state" | Out-Null
`)
}

func writeRunnerWorkspaceEnv(b *strings.Builder, workDir, toolsDir, stateDir string) {
	fmt.Fprintf(b, "WULING_RUNNER_WORK_DIR=%s\n", workDir)
	fmt.Fprintf(b, "WULING_RUNNER_TOOLS_DIR=%s\n", toolsDir)
	fmt.Fprintf(b, "WULING_RUNNER_STATE_DIR=%s\n", stateDir)
}

func writeTierResourceLimits(b *strings.Builder, tier TierSpec) {
	cpus, memory := tier.ContainerLimits()
	if cpus == 0 && memory == "" {
		return
	}
	if cpus > 0 {
		fmt.Fprintf(b, "WULING_RUNNER_CPUS=%d\n", cpus)
	}
	if memory != "" {
		fmt.Fprintf(b, "WULING_RUNNER_MEMORY=%s\n", memory)
	}
	b.WriteString("WULING_RUNNER_PIDS_LIMIT=4096\n")
}
