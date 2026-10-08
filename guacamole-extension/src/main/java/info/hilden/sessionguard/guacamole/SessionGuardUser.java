package info.hilden.sessionguard.guacamole;

import java.util.Collections;
import java.util.Set;

import org.apache.guacamole.net.auth.AbstractAuthenticatedUser;
import org.apache.guacamole.net.auth.AuthenticationProvider;
import org.apache.guacamole.net.auth.Credentials;

/**
 * A user authenticated through the SessionGuard ForwardAuth headers. The
 * effective user groups come from SessionGuard (OIDC groups after mapping);
 * the JDBC extension matches them by name against Guacamole user groups and
 * applies their permissions, so no manual group membership is required.
 */
final class SessionGuardUser extends AbstractAuthenticatedUser {

    private final AuthenticationProvider provider;
    private final Credentials credentials;
    private final Set<String> groups;

    SessionGuardUser(AuthenticationProvider provider, Credentials credentials, String username, Set<String> groups) {
        this.provider = provider;
        this.credentials = credentials;
        this.groups = Collections.unmodifiableSet(groups);
        setIdentifier(username);
    }

    @Override
    public AuthenticationProvider getAuthenticationProvider() {
        return provider;
    }

    @Override
    public Credentials getCredentials() {
        return credentials;
    }

    @Override
    public Set<String> getEffectiveUserGroups() {
        return groups;
    }
}
