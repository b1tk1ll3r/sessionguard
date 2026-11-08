package info.hilden.sessionguard.guacamole;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Map;
import java.util.Set;

import org.junit.jupiter.api.Test;

class SessionGuardAuthenticationProviderTest {

    private static final byte[] SECRET = "0123456789abcdef0123456789abcdef".getBytes(StandardCharsets.UTF_8);

    @Test
    void proxySecretMustMatchExactly() {
        assertTrue(SessionGuardAuthenticationProvider.secretMatches(SECRET, "0123456789abcdef0123456789abcdef"));
        assertTrue(SessionGuardAuthenticationProvider.secretMatches(SECRET, " 0123456789abcdef0123456789abcdef "));
        assertFalse(SessionGuardAuthenticationProvider.secretMatches(SECRET, null));
        assertFalse(SessionGuardAuthenticationProvider.secretMatches(SECRET, ""));
        assertFalse(SessionGuardAuthenticationProvider.secretMatches(SECRET, "0123456789abcdef0123456789abcde"));
        assertFalse(SessionGuardAuthenticationProvider.secretMatches(SECRET, "0123456789ABCDEF0123456789abcdef"));
    }

    @Test
    void withoutSecretEveryRequestIsAccepted() {
        assertTrue(SessionGuardAuthenticationProvider.secretMatches(null, null));
    }

    @Test
    void brokerTokensKeepOnlySessionGuardTokens() {
        Map<String, String> in = new HashMap<>();
        in.put("SESSIONGUARD_HOST", "rds01.example.local");
        in.put("SESSIONGUARD_REMOTE_APP", "||calc");
        in.put("GUAC_USERNAME", "administrator");
        in.put("GUAC_PASSWORD", "x");
        in.put("sessionguard_host", "evil");
        in.put("SESSIONGUARD_", "empty");
        Map<String, String> out = SessionGuardAuthenticationProvider.brokerTokens(in);
        assertEquals(Set.of("SESSIONGUARD_HOST", "SESSIONGUARD_REMOTE_APP"), out.keySet());
        assertEquals("rds01.example.local", out.get("SESSIONGUARD_HOST"));
    }

    @Test
    void parseGroupsDecodesAndRejectsMalformed() {
        Set<String> g = SessionGuardAuthenticationProvider.parseGroups("guac-users,RDS%20Admins,%ZZ,a+b, ,%0Abad");
        assertEquals(Set.of("guac-users", "RDS Admins"), g);
    }
}
