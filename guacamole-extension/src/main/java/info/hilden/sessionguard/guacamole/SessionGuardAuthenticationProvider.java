package info.hilden.sessionguard.guacamole;

import java.util.Arrays;
import java.util.Collections;
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
 * the actual authentication/storage providers. PocketID/header authentication
 * remains authoritative; this extension performs no user authentication.
 *
 * It does, however, veto every login that was not authenticated through the
 * SessionGuard identity header. Without this, the JDBC provider would still
 * accept username/password logins (e.g. the initdb default "guacadmin")
 * from anyone who passed ForwardAuth with any PocketID account.
 */
public final class SessionGuardAuthenticationProvider extends AbstractAuthenticationProvider {

    private static final Logger logger = LoggerFactory.getLogger(SessionGuardAuthenticationProvider.class);

    private final BrokerClient broker = BrokerClient.fromEnvironment();

    private final boolean enforceHeaderAuth = !"false".equalsIgnoreCase(env("SESSIONGUARD_ENFORCE_HEADER_AUTH", "true"));

    private final String identityHeader = env("SESSIONGUARD_IDENTITY_HEADER", env("HTTP_AUTH_HEADER", "X-Guacamole-User"));

    private final Set<String> allowedProviders = Collections.unmodifiableSet(
            Arrays.stream(env("SESSIONGUARD_ALLOWED_AUTH_PROVIDERS", "header").split(","))
                    .map(String::trim).filter(s -> !s.isEmpty())
                    .map(s -> s.toLowerCase(Locale.ROOT)).collect(Collectors.toSet()));

    @Override
    public String getIdentifier() {
        return "sessionguard-broker";
    }

    @Override
    public UserContext getUserContext(AuthenticatedUser authenticatedUser) throws GuacamoleException {
        if (enforceHeaderAuth && authenticatedUser != null) {
            String provider = authenticatedUser.getAuthenticationProvider() == null ? ""
                    : authenticatedUser.getAuthenticationProvider().getIdentifier();
            Credentials credentials = authenticatedUser.getCredentials();
            String header = credentials == null ? null : credentials.getHeader(identityHeader);
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
                return broker.resolve(username, connection.getIdentifier(), connection.getName());
            }
        };
    }

    private static String env(String name, String fallback) {
        String value = System.getenv(name);
        return value == null || value.isBlank() ? fallback : value.trim();
    }
}
