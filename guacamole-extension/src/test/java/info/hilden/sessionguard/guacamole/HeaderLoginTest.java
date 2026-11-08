package info.hilden.sessionguard.guacamole;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.lang.reflect.Proxy;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.Set;

import javax.servlet.http.HttpServletRequest;

import org.apache.guacamole.net.auth.AbstractAuthenticatedUser;
import org.apache.guacamole.net.auth.AbstractAuthenticationProvider;
import org.apache.guacamole.net.auth.AuthenticatedUser;
import org.apache.guacamole.net.auth.AuthenticationProvider;
import org.apache.guacamole.net.auth.Credentials;
import org.apache.guacamole.net.auth.credentials.GuacamoleInvalidCredentialsException;
import org.junit.jupiter.api.Test;

/**
 * Exercises the provider as Guacamole does: credentials built from an HTTP
 * request. The environment (see pom.xml) enables header login with the proxy
 * secret 0123456789abcdef0123456789abcdef.
 */
class HeaderLoginTest {

    private static final String SECRET = "0123456789abcdef0123456789abcdef";

    private final SessionGuardAuthenticationProvider provider = new SessionGuardAuthenticationProvider();

    private static Credentials request(Map<String, String> headers) {
        Map<String, String> h = new HashMap<>();
        headers.forEach((k, v) -> h.put(k.toLowerCase(), v));
        HttpServletRequest req = (HttpServletRequest) Proxy.newProxyInstance(HeaderLoginTest.class.getClassLoader(),
                new Class<?>[] { HttpServletRequest.class }, (p, m, a) -> {
                    switch (m.getName()) {
                        case "getHeader":
                            return h.get(((String) a[0]).toLowerCase());
                        case "getHeaders":
                            String v = h.get(((String) a[0]).toLowerCase());
                            return Collections.enumeration(v == null ? Collections.emptyList() : Collections.singletonList(v));
                        case "getHeaderNames":
                            return Collections.enumeration(h.keySet());
                        case "getParameterNames":
                            return Collections.emptyEnumeration();
                        case "getParameterMap":
                            return Collections.emptyMap();
                        case "getRemoteAddr":
                        case "getRemoteHost":
                            return "127.0.0.1";
                        default:
                            return null;
                    }
                });
        return new Credentials(null, null, req);
    }

    private static Map<String, String> headers(String... kv) {
        Map<String, String> m = new HashMap<>();
        for (int i = 0; i < kv.length; i += 2) {
            m.put(kv[i], kv[i + 1]);
        }
        return m;
    }

    @Test
    void loginWithProxySecret() throws Exception {
        AuthenticatedUser u = provider.authenticateUser(request(headers("X-Guacamole-User", "mmuster",
                "X-Guacamole-Groups", "guac-power,RDS%20Admins", "X-SessionGuard-Proxy-Auth", SECRET)));
        assertNotNull(u);
        assertEquals("mmuster", u.getIdentifier());
        assertEquals(Set.of("guac-power", "RDS Admins"), u.getEffectiveUserGroups());
    }

    @Test
    void identityHeaderWithoutOrWithWrongSecretIsIgnored() throws Exception {
        assertNull(provider.authenticateUser(request(headers("X-Guacamole-User", "guacadmin"))));
        assertNull(provider.authenticateUser(request(headers("X-Guacamole-User", "guacadmin", "X-SessionGuard-Proxy-Auth", "wrong"))));
        assertNull(provider.authenticateUser(request(headers("X-Guacamole-User", "guacadmin", "X-SessionGuard-Proxy-Auth", ""))));
    }

    @Test
    void existingSessionEndsWhenIdentityIsMissingOrChanges() throws Exception {
        AuthenticatedUser u = provider.authenticateUser(request(headers("X-Guacamole-User", "mmuster", "X-SessionGuard-Proxy-Auth", SECRET)));
        // same user with secret: session continues
        AuthenticatedUser same = provider.updateAuthenticatedUser(u, request(headers("X-Guacamole-User", "mmuster", "X-SessionGuard-Proxy-Auth", SECRET)));
        assertEquals("mmuster", same.getIdentifier());
        // no identity (access session ended), no secret (proxy bypassed), other user
        assertThrows(GuacamoleInvalidCredentialsException.class, () -> provider.updateAuthenticatedUser(u, request(headers())));
        assertThrows(GuacamoleInvalidCredentialsException.class,
                () -> provider.updateAuthenticatedUser(u, request(headers("X-Guacamole-User", "mmuster"))));
        assertThrows(GuacamoleInvalidCredentialsException.class,
                () -> provider.updateAuthenticatedUser(u, request(headers("X-Guacamole-User", "other", "X-SessionGuard-Proxy-Auth", SECRET))));
    }

    @Test
    void loginsOfOtherProvidersAreVetoed() throws Exception {
        AuthenticationProvider jdbc = new AbstractAuthenticationProvider() {
            @Override
            public String getIdentifier() {
                return "postgresql";
            }
        };
        Credentials creds = request(headers("X-Guacamole-User", "guacadmin", "X-SessionGuard-Proxy-Auth", SECRET));
        AbstractAuthenticatedUser viaJdbc = new AbstractAuthenticatedUser() {
            @Override
            public AuthenticationProvider getAuthenticationProvider() {
                return jdbc;
            }

            @Override
            public Credentials getCredentials() {
                return creds;
            }
        };
        viaJdbc.setIdentifier("guacadmin");
        assertThrows(GuacamoleInvalidCredentialsException.class, () -> provider.getUserContext(viaJdbc));

        AuthenticatedUser viaHeader = provider.authenticateUser(creds);
        assertSame(null, provider.getUserContext(viaHeader));
    }
}
