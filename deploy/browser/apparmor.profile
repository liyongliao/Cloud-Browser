#include <tunables/global>
profile cloud-browser flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>
  file,
  network,
  capability,
  signal,
  ptrace (read, trace) peer=cloud-browser,
  userns,
  deny /sys/** wklx,
  deny /proc/sys/** wklx,
  deny /proc/sysrq-trigger rwklx,
}
