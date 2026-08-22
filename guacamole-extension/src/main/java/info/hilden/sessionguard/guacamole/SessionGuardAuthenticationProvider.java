package info.hilden.sessionguard.guacamole;

import java.util.Map;

import org.apache.guacamole.GuacamoleException;
import org.apache.guacamole.net.auth.AbstractAuthenticationProvider;
import org.apache.guacamole.net.auth.AuthenticatedUser;
import org.apache.guacamole.net.auth.Connection;
import org.apache.guacamole.net.auth.Credentials;
import org.apache.guacamole.net.auth.TokenInjectingUserContext;
import org.apache.guacamole.net.auth.UserContext;

/**
 * Adds broker-selected Guacamole connection tokens to UserContexts created by
 * the actual authentication/storage providers. PocketID/header authentication
 * remains authoritative; this extension performs no user authentication.
 */
public final class SessionGuardAuthenticationProvider extends AbstractAuthenticationProvider {

    private final BrokerClient broker = BrokerClient.fromEnvironment();

    @Override
    public String getIdentifier() {
        return "sessionguard-broker";
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
}
