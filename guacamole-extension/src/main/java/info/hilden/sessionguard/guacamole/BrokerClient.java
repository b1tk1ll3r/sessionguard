package info.hilden.sessionguard.guacamole;

import java.io.IOException;
import java.net.HttpURLConnection;
import java.net.URI;
import java.net.URLEncoder;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;

import org.apache.guacamole.GuacamoleException;

final class BrokerClient {
    private final URI endpoint;
    private final String apiKey;
    private final int timeoutMs;

    private BrokerClient(URI endpoint, String apiKey, int timeoutMs) {
        this.endpoint = endpoint;
        this.apiKey = apiKey;
        this.timeoutMs = timeoutMs;
    }

    static BrokerClient fromEnvironment() {
        String base = envRequired("SESSIONGUARD_MASTER_URL").replaceAll("/+$", "");
        String key = envRequired("SESSIONGUARD_BROKER_API_KEY");
        int timeout = 2500;
        String rawTimeout = System.getenv("SESSIONGUARD_BROKER_TIMEOUT_MS");
        if (rawTimeout != null && !rawTimeout.isBlank()) {
            try { timeout = Math.max(250, Integer.parseInt(rawTimeout)); }
            catch (NumberFormatException ignored) { }
        }
        return new BrokerClient(URI.create(base + "/api/v1/broker/tokens"), key, timeout);
    }

    Map<String, String> resolve(String username, String connectionId, String connectionName)
            throws GuacamoleException {
        String body = "username=" + enc(username)
                + "&connection_id=" + enc(connectionId)
                + "&connection_name=" + enc(connectionName);
        try {
            HttpURLConnection c = (HttpURLConnection) endpoint.toURL().openConnection();
            c.setRequestMethod("POST");
            c.setConnectTimeout(timeoutMs);
            c.setReadTimeout(timeoutMs);
            c.setDoOutput(true);
            c.setRequestProperty("Authorization", "Bearer " + apiKey);
            c.setRequestProperty("Content-Type", "application/x-www-form-urlencoded");
            c.setRequestProperty("Accept", "application/x-www-form-urlencoded");
            c.getOutputStream().write(body.getBytes(StandardCharsets.UTF_8));
            int status = c.getResponseCode();
            if (status != 200) {
                String msg = c.getErrorStream() == null ? "" : new String(c.getErrorStream().readAllBytes(), StandardCharsets.UTF_8);
                throw new GuacamoleException("SessionGuard broker returned HTTP " + status + ": " + msg);
            }
            String response = new String(c.getInputStream().readAllBytes(), StandardCharsets.UTF_8);
            return parseForm(response);
        }
        catch (IOException e) {
            throw new GuacamoleException("SessionGuard broker is unavailable", e);
        }
    }

    private static Map<String, String> parseForm(String input) {
        if (input == null || input.isBlank()) return Collections.emptyMap();
        Map<String, String> out = new HashMap<>();
        for (String pair : input.split("&")) {
            int eq = pair.indexOf('=');
            String key = eq < 0 ? pair : pair.substring(0, eq);
            String value = eq < 0 ? "" : pair.substring(eq + 1);
            out.put(dec(key), dec(value));
        }
        return out;
    }

    private static String enc(String value) {
        return URLEncoder.encode(value == null ? "" : value, StandardCharsets.UTF_8);
    }

    private static String dec(String value) {
        return URLDecoder.decode(value, StandardCharsets.UTF_8);
    }

    private static String envRequired(String name) {
        String value = System.getenv(name);
        if (value == null || value.isBlank()) {
            throw new IllegalStateException(name + " is required by SessionGuard Guacamole extension");
        }
        return value.trim();
    }
}
