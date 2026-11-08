package info.hilden.sessionguard.guacamole;

import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashMap;
import java.util.LinkedHashSet;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.stream.Collectors;

import org.apache.guacamole.GuacamoleException;
import org.apache.guacamole.net.auth.AbstractAuthenticationProvider;
import org.apache.guacamole.net.auth.AuthenticatedUser;
import org.apache.guacamole.net.auth.Connection;
import org.apache.guacamole.net.auth.Credentials;
import org.apache.guacamole.net.auth.TokenInjectingUserContext;
import org.apache.guacamole.net.auth.UserContext;
import org.apache.guacamole.net.auth.credentials.CredentialsInfo;
import org.apache.guacamole.net.auth.credentials.GuacamoleInvalidCredentialsException;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * Adds broker-selected Guacamole connection tokens to UserContexts created by
 * the actual authentication/storage providers.
 *
 * With SESSIONGUARD_HEADER_LOGIN=true it also authenticates users itself from
 * the SessionGuard ForwardAuth headers (replacing guacamole-auth-header) and
 * passes the user's mapped OIDC groups (X-Guacamole-Groups) as effective
 * user groups. The JDBC extension matches them by name against Guacamole
 * user groups, so group permissions apply without manual membership.
 *
 * It does, however, veto every login that was not authenticated through the
 * SessionGuard identity header. Without this, the JDBC provider would still
 * accept username/password logins (e.g. the initdb default "guacadmin")
 * from anyone who passed ForwardAuth with any IdP account.
 *
 * With SESSIONGUARD_PROXY_SECRET set, identity headers are only trusted on
 * requests that also carry that secret in SESSIONGUARD_PROXY_SECRET_HEADER
 * (default X-SessionGuard-Proxy-Auth). The reverse proxy sets it after
 * ForwardAuth, so a client that reaches Guacamole directly – bypassing the
 * proxy – cannot log in as anybody by sending X-Guacamole-User itself.
 */
public final class SessionGuardAuthenticationProvider extends AbstractAuthenticationProvider {

    private static final Logger logger = LoggerFactory.getLogger(SessionGuardAuthenticationProvider.class);

    private final BrokerClient broker = BrokerClient.fromEnvironment();

    private final boolean enforceHeaderAuth = !"false".equalsIgnoreCase(env("SESSIONGUARD_ENFORCE_HEADER_AUTH", "true"));

    private final String identityHeader = env("SESSIONGUARD_IDENTITY_HEADER", env("HTTP_AUTH_HEADER", "X-Guacamole-User"));

    // Opt-in: trusting identity headers is only safe behind the SessionGuard
    // ForwardAuth proxy, which strips client-supplied copies.
    private final boolean headerLogin = "true".equalsIgnoreCase(env("SESSIONGUARD_HEADER_LOGIN", "false"));

    private final String groupsHeader = env("SESSIONGUARD_GROUPS_HEADER", "X-Guacamole-Groups");

    private final String proxySecretHeader = env("SESSIONGUARD_PROXY_SECRET_HEADER", "X-SessionGuard-Proxy-Auth");

    private final byte[] proxySecret = proxySecretFromEnvironment();

    private final Set<String> allowedProviders = Collections.unmodifiableSet(
            Arrays.stream(env("SESSIONGUARD_ALLOWED_AUTH_PROVIDERS", "header,sessionguard-broker").split(","))
                    .map(String::trim).filter(s -> !s.isEmpty())
                    .map(s -> s.toLowerCase(Locale.ROOT)).collect(Collectors.toSet()));

    @Override
    public String getIdentifier() {
        return "sessionguard-broker";
    }

    private static byte[] proxySecretFromEnvironment() {
        String v = System.getenv("SESSIONGUARD_PROXY_SECRET");
        if (v == null || v.isBlank()) {
            logger.warn("SESSIONGUARD_PROXY_SECRET is not set: identity headers are trusted from every client that "
                    + "reaches Guacamole. Make sure Guacamole is reachable only through the reverse proxy.");
            return null;
        }
        v = v.trim();
        if (v.length() < 24) {
            throw new IllegalStateException("SESSIONGUARD_PROXY_SECRET must be at least 24 characters");
        }
        return v.getBytes(StandardCharsets.UTF_8);
    }

    /**
     * Reports whether the request came through the trusted reverse proxy.
     * Without a configured secret every request is accepted (previous
     * behavior; the proxy must then be the only network path).
     */
    boolean fromTrustedProxy(Credentials credentials) {
        return secretMatches(proxySecret, credentials == null ? null : credentials.getHeader(proxySecretHeader));
    }

    /** Constant-time comparison; a null secret means "not configured". */
    static boolean secretMatches(byte[] secret, String got) {
        if (secret == null) {
            return true;
        }
        if (got == null) {
            return false;
        }
        return MessageDigest.isEqual(secret, got.trim().getBytes(StandardCharsets.UTF_8));
    }

    @Override
    public AuthenticatedUser authenticateUser(Credentials credentials) throws GuacamoleException {
        if (!headerLogin || credentials == null) {
            return null;
        }
        if (!fromTrustedProxy(credentials)) {
            if (credentials.getHeader(identityHeader) != null) {
                logger.warn("SessionGuard ignored identity header from a request without valid proxy secret ({}).",
                        credentials.getRemoteAddress());
            }
            return null;
        }
        String username = trimmed(credentials.getHeader(identityHeader));
        if (username == null) {
            return null;
        }
        Set<String> groups = parseGroups(credentials.getHeader(groupsHeader));
        logger.debug("SessionGuard header login for \"{}\" with groups {}", username, groups);
        return new SessionGuardUser(this, credentials, username, groups);
    }

    @Override
    public AuthenticatedUser updateAuthenticatedUser(AuthenticatedUser authenticatedUser, Credentials credentials)
            throws GuacamoleException {
        if (!(authenticatedUser instanceof SessionGuardUser) || credentials == null) {
            return authenticatedUser;
        }
        String username = fromTrustedProxy(credentials) ? trimmed(credentials.getHeader(identityHeader)) : null;
        if (username == null) {
            // Fail closed: a request with a Guacamole token but without a
            // valid SessionGuard identity did not pass ForwardAuth (expired or
            // revoked access session, or a client bypassing the proxy).
            logger.warn("SessionGuard identity missing for \"{}\"; invalidating Guacamole session.",
                    authenticatedUser.getIdentifier());
            throw new GuacamoleInvalidCredentialsException("SessionGuard identity missing.", CredentialsInfo.EMPTY);
        }
        // The browser kept a Guacamole token, but the SessionGuard access
        // session now belongs to someone else: never continue as the old user.
        if (!username.equalsIgnoreCase(authenticatedUser.getIdentifier())) {
            logger.warn("SessionGuard identity changed from \"{}\" to \"{}\"; invalidating Guacamole session.",
                    authenticatedUser.getIdentifier(), username);
            throw new GuacamoleInvalidCredentialsException("SessionGuard identity changed.", CredentialsInfo.EMPTY);
        }
        // Refresh groups from the current access session.
        return new SessionGuardUser(this, credentials, authenticatedUser.getIdentifier(),
                parseGroups(credentials.getHeader(groupsHeader)));
    }

    /**
     * Parses the comma-separated, percent-encoded group list emitted by the
     * SessionGuard Master. Malformed entries are skipped.
     */
    static Set<String> parseGroups(String header) {
        Set<String> out = new LinkedHashSet<>();
        if (header == null || header.isBlank()) {
            return out;
        }
        for (String part : header.split(",")) {
            String p = part.trim();
            if (p.isEmpty() || p.indexOf('+') >= 0) {
                continue;
            }
            try {
                String raw = URLDecoder.decode(p, StandardCharsets.UTF_8);
                String name = raw.trim();
                if (!name.isEmpty() && name.equals(raw) && name.length() <= 128 && raw.chars().noneMatch(Character::isISOControl)) {
                    out.add(name);
                }
            }
            catch (IllegalArgumentException ignored) {
                // malformed percent-encoding
            }
            if (out.size() >= 200) {
                break;
            }
        }
        return out;
    }

    private static String trimmed(String v) {
        if (v == null) {
            return null;
        }
        v = v.trim();
        return v.isEmpty() ? null : v;
    }

    @Override
    public UserContext getUserContext(AuthenticatedUser authenticatedUser) throws GuacamoleException {
        if (enforceHeaderAuth && authenticatedUser != null) {
            String provider = authenticatedUser.getAuthenticationProvider() == null ? ""
                    : authenticatedUser.getAuthenticationProvider().getIdentifier();
            Credentials credentials = authenticatedUser.getCredentials();
            String header = credentials == null || !fromTrustedProxy(credentials) ? null : credentials.getHeader(identityHeader);
            boolean viaHeader = provider != null && allowedProviders.contains(provider.toLowerCase(Locale.ROOT));
            boolean headerMatches = header != null && header.trim().equalsIgnoreCase(authenticatedUser.getIdentifier());
            if (!viaHeader || !headerMatches) {
                logger.warn("SessionGuard vetoed Guacamole login of \"{}\" via provider \"{}\": only identities "
                        + "authenticated by the SessionGuard header are allowed.", authenticatedUser.getIdentifier(), provider);
                throw new GuacamoleInvalidCredentialsException("Login is only possible through SessionGuard.",
                        CredentialsInfo.USERNAME_PASSWORD);
            }
        }
        // This provider stores no users or connections itself.
        return null;
    }

    @Override
    public UserContext decorate(UserContext context, AuthenticatedUser authenticatedUser,
                                Credentials credentials) throws GuacamoleException {
        if (context == null || authenticatedUser == null) {
            return context;
        }

        final String username = authenticatedUser.getIdentifier();
        return new TokenInjectingUserContext(context) {
            @Override
            protected Map<String, String> getTokens(Connection connection) throws GuacamoleException {
                return brokerTokens(broker.resolve(username, connection.getIdentifier(), connection.getName()));
            }
        };
    }

    /**
     * Keeps only SESSIONGUARD_* tokens from the broker answer, so a broker
     * response can never override Guacamole's own tokens such as
     * GUAC_USERNAME or GUAC_PASSWORD.
     */
    static Map<String, String> brokerTokens(Map<String, String> in) {
        Map<String, String> out = new HashMap<>();
        if (in == null) {
            return out;
        }
        for (Map.Entry<String, String> e : in.entrySet()) {
            String k = e.getKey();
            if (k != null && k.matches("SESSIONGUARD_[A-Z0-9_]{1,64}") && e.getValue() != null) {
                out.put(k, e.getValue());
            }
        }
        return out;
    }

    private static String env(String name, String fallback) {
        String value = System.getenv(name);
        return value == null || value.isBlank() ? fallback : value.trim();
    }
}
