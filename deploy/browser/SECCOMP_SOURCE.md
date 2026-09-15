# Chrome sandbox seccomp profile

Derived from <https://github.com/moby/profiles/blob/main/seccomp/default.json>, retrieved 2026-09-14.

Changes: removed conditional rules for `clone`, `clone3`, `unshare`, and `setns`; allowed these calls so Chrome can construct its unprivileged namespace sandbox. Other upstream default restrictions are retained. Apply together with the named AppArmor profile, a non-root user, dropped capabilities and the dedicated browser network.

The upstream Apache-2.0 license is included in `SECCOMP_LICENSE`. This configuration must be tested against the target kernel; it is not a claim that every container runtime supports Chrome sandboxing.
