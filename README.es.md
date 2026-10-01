# nextcloud-mcp-fast

[![CI](https://github.com/valdrent/nextcloud-mcp-fast/actions/workflows/ci.yml/badge.svg)](https://github.com/valdrent/nextcloud-mcp-fast/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/valdrent/nextcloud-mcp-fast.svg)](https://pkg.go.dev/github.com/valdrent/nextcloud-mcp-fast)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

[English](README.md) | [Español](README.es.md)

Un servidor **MCP (Model Context Protocol)** ligero y de código abierto que da a los clientes LLM acceso a los archivos de [Nextcloud](https://nextcloud.com) mediante WebDAV. Escrito en Go para tener una huella de memoria mínima (~20–50 MB en reposo) y un único binario estático que puedes ejecutar en cualquier parte. Mantenido por [Valdrent](https://github.com/valdrent) y la comunidad.

Está diseñado para ser **multi-inquilino**: un solo proceso puede atender muchas cuentas de Nextcloud, resolviendo las credenciales por cuenta bajo demanda (pass-through), compartiendo una única pool de conexiones HTTP y sin mantener estado pesado por usuario.

> **Estado:** desarrollo temprano (pre-1.0) — la superficie de API es estable para uso local pero aún puede evolucionar; los cambios incompatibles se listan en el [CHANGELOG](CHANGELOG.md). El modo multi-cuenta pass-through es **experimental** y aún no se recomienda en producción (ver [Modelo de seguridad](#modelo-de-seguridad)). Las contribuciones son bienvenidas; ver [Contribuir](#contribuir).

## Por qué existe este proyecto

Nextcloud ya expone archivos mediante WebDAV, pero no existía una forma pequeña y segura de entregarlo a un agente LLM. Las opciones existentes eran pesadas, de un solo usuario, o daban al modelo acceso irrestricto a un servidor. Este proyecto busca ser:

- **Pequeño** — un único binario estático, poca RAM, trivialmente contenerizado.
- **Seguro por defecto** — solo lectura salvo que lo actives; cada ruta está encerrada (jailed); los bucles descontrolados se cortan con un disyuntor (circuit breaker).
- **Honesto sobre seguridad** — *no* reemplaza las ACLs ni la autenticación propias de Nextcloud; añade una barrera local por encima de una App Password.

## Características

- **8 herramientas MCP**: `list_files`, `read_file`, `write_file`, `create_folder`,
  `move_file`, `delete`, `search_files`, `stat`.
- **Dos transportes**: `stdio` (clientes locales como Claude Desktop) y
  `streamable-http` (despliegues remotos), con tres modos de autenticación para
  HTTP: token Bearer compartido (`static`, por defecto) o tokens de acceso
  OAuth 2.0 (`oidc` / `nextcloud`) para clientes en la nube como Claude Cowork.
- **Seguridad por defecto**:
  - *Encierro de rutas (path jail)* — decodificación percent recursiva + normalización NFC +
    verificación léxica de contención. El recorrido (`..`, `%2e%2e` codificado o doblemente codificado, bytes NUL,
    UTF-8 inválido) se rechaza antes de que cualquier solicitud salga del proceso.
  - *Guardián de permisos* — niveles `read` / `write` / `destructive`; las operaciones destructivas
    están desactivadas salvo que se activen explícitamente.
  - *Disyuntor (circuit breaker)* — límite de tasa por ventana deslizante por cuenta para detener
    bucles LLM descontrolados que martilleen tu Nextcloud.
- **Poca RAM**: un único `http.Client` compartido entre todas las cuentas, pool de conexiones acotado,
  sin tormentas de goroutines por usuario.
- **Listo para Docker**: build multi-etapa sobre una imagen base distroless con `--healthcheck` integrado.

## Inicio rápido

### Instalación

```sh
go install github.com/valdrent/nextcloud-mcp-fast@latest
# o descarga la imagen de contenedor
docker pull ghcr.io/valdrent/nextcloud-mcp-fast:latest
```

### Compilar y ejecutar (stdio)

```sh
git clone https://github.com/valdrent/nextcloud-mcp-fast.git && cd nextcloud-mcp-fast
make build
export NEXTCLOUD_HOST=https://cloud.example.com
export NEXTCLOUD_USERNAME=tu_usuario
export NEXTCLOUD_PASSWORD=<App Password>   # Ajustes → Seguridad → Contraseñas de app
./bin/nextcloud-mcp-fast
```

### Docker

```sh
docker compose up -d
# o
make docker VERSION=v1.0.0
```

Ver [`docker-compose.yml`](docker-compose.yml) para un ejemplo de contenedor con memoria acotada y solo lectura.

### Verificar la imagen

Las imágenes Docker están firmadas con [cosign](https://docs.sigstore.dev/cosign/). Verifica la firma antes de usarla:

```sh
cosign verify ghcr.io/valdrent/nextcloud-mcp-fast:v1.0.0 \
  --certificate-identity-regexp 'https://github.com/valdrent/nextcloud-mcp-fast/.github/workflows/release.yml@refs/tags/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Esto garantiza que la imagen fue construida por el flujo de trabajo de lanzamiento y no ha sido alterada. La firma sin claves usa el token OIDC de GitHub, por lo que no hay claves almacenadas ni rotadas.

## Configuración (variables de entorno)

| Variable | Obligatoria | Por defecto | Descripción |
|---|---|---|---|
| `NEXTCLOUD_HOST` | sí | — | URL base de Nextcloud, p. ej. `https://cloud.example.com` |
| `NEXTCLOUD_USERNAME` | * | — | Nombre de usuario de la cuenta (omitir si el pass-through está activo) |
| `NEXTCLOUD_PASSWORD` | * | — | **App Password** (no tu contraseña de acceso) |
| `NEXTCLOUD_MCP_TRANSPORT` | no | `stdio` | `stdio` o `http` |
| `NEXTCLOUD_MCP_HTTP_ADDR` | no | `127.0.0.1:8000` | Dirección de escucha en modo HTTP (la imagen Docker usa `:8000`) |
| `NEXTCLOUD_MCP_HTTP_TOKEN` | modo http, auth `static` | — | Token Bearer, mínimo 32 caracteres (p. ej. `openssl rand -hex 32`); los clientes envían `Authorization: Bearer <token>`. No se usa en modos OAuth |
| `NEXTCLOUD_MCP_ALLOWED_HOSTS` | no | — | Lista blanca separada por comas de `scheme://host[:puerto]` para pass-through; `NEXTCLOUD_HOST` siempre está permitido; las entradas `http://` solo si el host por defecto es `http://` |
| `NEXTCLOUD_MCP_PERMISSIONS` | no | `read` | `read`, `write` o `destructive` (alias `full`) |
| `NEXTCLOUD_MCP_PASSTHROUGH` | no | `false` | Permite credenciales por solicitud (multi-cuenta); requiere modo `http`. Las credenciales solo llegan por las cabeceras `X-Nextcloud-Host`/`X-Nextcloud-Username`/`X-Nextcloud-Password` |
| `NEXTCLOUD_MCP_MAX_READ_BYTES` | no | `131072` | Límite para una sola llamada a `read_file` |
| `NEXTCLOUD_MCP_MAX_LIST_ENTRIES` | no | `50` | Entradas por página de `list_files` (máx. 200) |
| `NEXTCLOUD_MCP_HTTP_TIMEOUT` | no | `30s` | Timeout WebDAV por solicitud |
| `NEXTCLOUD_MCP_CB_THRESHOLD` | no | `10` | Llamadas fallidas dentro de la ventana antes de disparar el disyuntor (`0` lo desactiva) |
| `NEXTCLOUD_MCP_CB_WINDOW` | no | `1m` | Duración de la ventana deslizante del disyuntor |
| `NEXTCLOUD_MCP_LOG_LEVEL` | no | `info` | Verbosidad del registro de auditoría JSON: `debug`, `info`, `warn` o `error`; registra cada llamada de herramienta (herramienta, cuenta, rutas, resultado, duración) en stderr |
| `NEXTCLOUD_MCP_AUTH_MODE` | no | `static` | Modo de autenticación HTTP: `static` (Bearer compartido), `oidc` o `nextcloud` (tokens de acceso OAuth 2.0; necesario para Claude Cowork/claude.ai). Los modos OAuth requieren transporte `http` y `NEXTCLOUD_MCP_PUBLIC_URL`. Ver [docs/oauth-oidc.md](docs/oauth-oidc.md) y [docs/oauth-nextcloud.md](docs/oauth-nextcloud.md) |
| `NEXTCLOUD_MCP_PUBLIC_URL` | modos OAuth | — | URL https pública del endpoint MCP (identificador de recurso OAuth y audiencia exigida al token; http solo en loopback) |
| `NEXTCLOUD_MCP_OIDC_ISSUER` | oidc | — | URL del emisor (issuer) del proveedor OIDC (https; http solo en loopback) |
| `NEXTCLOUD_MCP_ACCOUNTS_FILE` | oidc | — | Archivo JSON (modo 0600) que asigna usuarios OAuth a App Passwords de Nextcloud; opcional como anulación en modo `nextcloud` |

\* Obligatoria salvo que `NEXTCLOUD_MCP_PASSTHROUGH=true` o se use un modo de autenticación OAuth.

## Configuración del cliente MCP

Claude Desktop (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "nextcloud": {
      "command": "/ruta/hacia/nextcloud-mcp-fast",
      "env": {
        "NEXTCLOUD_HOST": "https://cloud.example.com",
        "NEXTCLOUD_USERNAME": "tu_usuario",
        "NEXTCLOUD_PASSWORD": "<App Password>",
        "NEXTCLOUD_MCP_PERMISSIONS": "read"
      }
    }
  }
}
```

Para clientes remotos/streamable-HTTP, apúntalos a `http://host:8000/mcp`.

### Claude Cowork / claude.ai (conector personalizado)

Claude se conecta desde la nube de Anthropic y para servidores remotos solo admite
OAuth 2.0, así que un token Bearer estático no sirve. Necesitas una URL **https**
pública y uno de dos modos OAuth:

| Modo | Servidor de autorización | Usuario → cuenta Nextcloud | Guía |
|---|---|---|---|
| `NEXTCLOUD_MCP_AUTH_MODE=oidc` | Tu propio IdP (Keycloak, Authentik, Auth0, Zitadel…), con DCR | `NEXTCLOUD_MCP_ACCOUNTS_FILE` (obligatorio) | [docs/oauth-oidc.md](docs/oauth-oidc.md) |
| `NEXTCLOUD_MCP_AUTH_MODE=nextcloud` *(experimental)* | App `oauth2` de Nextcloud; client ID/secret manuales en Claude | El usuario verificado + el token actúan directamente como credenciales WebDAV (archivo de cuentas opcional) | [docs/oauth-nextcloud.md](docs/oauth-nextcloud.md) |

En ambos modos el servidor es solo el *servidor de recursos*: publica los
metadatos RFC 9728 de recurso protegido, verifica cada token de acceso (`oidc`:
verificación local del JWT contra el JWKS del emisor; `nextcloud`: comprobación
contra la API OCS de Nextcloud, con caché de 60 s) y asigna el usuario
autenticado a una cuenta de Nextcloud. Los tokens se comprueban en cada
solicitud, así que la revocación surte efecto inmediato (dentro de la caché de
60 s en modo `nextcloud`).

Después añade el conector en *Settings → Connectors → Add custom connector* con la
URL `https://tu-dominio/mcp`. El callback OAuth a permitir es
`https://claude.ai/api/mcp/auth_callback`.

### Consumo real

En un despliegue en producción que sustituyó a un servidor MCP de Nextcloud en
Python, la RAM en reposo bajó de ~369 MB a ~7 MB (~98 % menos), sin token store,
volumen de datos ni secretos en el servidor. El coste es el alcance: solo 8
herramientas de archivos. La guía paso a paso, un Compose endurecido y la
resolución de problemas están en [docs/oauth-nextcloud.md](docs/oauth-nextcloud.md).

### GitHub Copilot (cloud agent y code review)

En el repositorio, ve a **Settings → Copilot → MCP servers** y pega:

```json
{
  "mcpServers": {
    "nextcloud": {
      "type": "local",
      "command": "docker",
      "args": [
        "run", "--rm", "-i", "--read-only", "--memory=256m",
        "-e", "NEXTCLOUD_HOST",
        "-e", "NEXTCLOUD_USERNAME",
        "-e", "NEXTCLOUD_PASSWORD",
        "-e", "NEXTCLOUD_MCP_PERMISSIONS",
        "ghcr.io/valdrent/nextcloud-mcp-fast:latest"
      ],
      "env": {
        "NEXTCLOUD_HOST": "$COPILOT_MCP_NEXTCLOUD_HOST",
        "NEXTCLOUD_USERNAME": "$COPILOT_MCP_NEXTCLOUD_USERNAME",
        "NEXTCLOUD_PASSWORD": "$COPILOT_MCP_NEXTCLOUD_PASSWORD",
        "NEXTCLOUD_MCP_PERMISSIONS": "read"
      },
      "tools": ["list_files", "read_file", "search_files", "stat"]
    }
  }
}
```

Luego crea lo siguiente en **Settings → Secrets and variables → Agents** (solo los
nombres que empiezan por `COPILOT_MCP_` son visibles para los servidores MCP):

| Nombre | Tipo | Valor |
|---|---|---|
| `COPILOT_MCP_NEXTCLOUD_HOST` | variable | `https://cloud.example.com` |
| `COPILOT_MCP_NEXTCLOUD_USERNAME` | secret | Usuario de Nextcloud |
| `COPILOT_MCP_NEXTCLOUD_PASSWORD` | secret | Una **App Password** dedicada |

Notas:

- La lista `tools` expone solo herramientas de lectura y el servidor corre con
  permiso `read`. Copilot code review solo usa herramientas de lectura en cualquier caso.
- Usa una cuenta o App Password de Nextcloud dedicada para Copilot, para poder
  revocarla de forma independiente.
- La configuración usa la imagen de contenedor publicada y firmada. Para
  configuraciones reproducibles, fija un tag de release (p. ej. `:v1.0.0`) en
  lugar de `:latest`.
- Para verificarlo, abre los logs de una sesión de Copilot y expande **Start MCP Servers**.

## Herramientas

| Herramienta | Permiso | Descripción |
|---|---|---|
| `list_files` | read | Lista un directorio (paginado vía `limit`/`offset`) |
| `read_file` | read | Lee un archivo (texto o base64 con `encoding=base64`); devuelve `unsupported_type` para contenido binario sin `encoding=base64`; lecturas parciales con `offset`/`length`; devuelve `server_error` si el servidor ignora una solicitud Range; resultados marcados `"trust":"untrusted"` |
| `write_file` | write | Crea un archivo; las carpetas padre se crean automáticamente si no existen; `overwrite` (default false) habilita reemplazar archivos existentes; las sobrescrituras requieren permiso `destructive` |
| `create_folder` | write | Crea un directorio |
| `move_file` | write | Renombra/mueve una ruta; `overwrite` (default false) para reemplazar un destino existente; las sobrescrituras requieren permiso `destructive` |
| `delete` | destructive | Elimina un archivo o carpeta — **Nextcloud elimina las carpetas de forma recursiva** (los elementos van a la papelera de Nextcloud si está habilitada) |
| `search_files` | read | Búsqueda de nombres con búsqueda WebDAV del lado del servidor (indexada), con alternativa a caminata de directorio acotada (máx. 500 carpetas, presupuesto de 60s) en servidores sin soporte SEARCH |
| `stat` | read | Metadatos para una única ruta |

## Modelo de seguridad

Lee esto antes de ejecutar con escritura habilitada.

- **Usa una App Password**, nunca la contraseña principal de tu cuenta. Puedes revocarla
  de forma independiente en cualquier momento.
- **El encierro de rutas es léxico y dentro del proceso.** El cliente WebDAV solo ve
  rutas saneadas bajo la raíz de archivos de la cuenta. *No* sustituye a la autorización
  del lado servidor de Nextcloud — si tu App Password puede alcanzar un archivo, este
  servidor también (dentro del nivel de permiso configurado).
- **Los niveles de permiso son un techo, no un suelo.** `read` no puede escribir sin
  importar lo que pida el LLM. Mantenlo en `read` salvo que necesites escritura específicamente.
- **El disyuntor protege tu servidor**, no tus datos: limita una cuenta malcomportada tras
  fallos repetidos.
- **El modo HTTP siempre exige autenticación**: un token Bearer compartido (`static`) o
  tokens de acceso OAuth (`oidc` / `nextcloud`). Quien tenga una credencial válida actúa
  con la cuenta asociada, así que enlázalo a `127.0.0.1` o ponlo detrás de un reverse
  proxy que termine TLS.
- **Los modos OAuth verifican cada token**: `oidc` comprueba firma (solo RS256/ES256),
  emisor, caducidad y que la audiencia sea tu `NEXTCLOUD_MCP_PUBLIC_URL`; los usuarios
  que no estén en el archivo de cuentas se deniegan y los scopes `mcp:*` solo pueden
  bajar el nivel de permisos. El archivo de cuentas contiene App Passwords: `chmod 600`.
  En modo `nextcloud`, el token se valida contra la API OCS de Nextcloud (caché de
  60 s) y un token revocado deja de funcionar en menos de un minuto.
- **Usa siempre `https://` en `NEXTCLOUD_HOST`.** Con `http://`, la App Password viaja
  en texto plano.
- **El modo pass-through es experimental.** Permite que los clientes elijan el host y
  las credenciales de Nextcloud por solicitud (incluso mediante argumentos de las
  herramientas, que el LLM puede ver). No lo actives en un servidor que también tenga
  credenciales por defecto configuradas, ni lo expongas a redes no confiables hasta
  que alcance estado estable.
- **Ejecuta contenedores endurecidos**: `read_only`, memoria acotada, usuario no-root (la
  imagen distroless ya corre como `nonroot`). Ver el archivo compose.

¿Encontraste una vulnerabilidad? Repórtala de forma privada — ver [SECURITY.md](SECURITY.md).

## Desarrollo

```sh
make build     # binario estático en bin/nextcloud-mcp-fast
make test      # go test ./...
make lint      # go vet + gofmt
make run       # go run .
make docker    # compila la imagen de contenedor
```

### Estructura del proyecto

```
main.go                  punto de entrada: config → registro → servidor MCP → transporte
internal/config/         carga y validación de env, análisis de permisos
internal/errors/         códigos de error semánticos para respuestas amigables al LLM
internal/sanitize/       encierro de rutas (decodificar, NFC, contención)
internal/perm/           guardián read/write/destructive
internal/breaker/        disyuntor por ventana deslizante por cuenta
internal/webdav/         cliente WebDAV + operaciones (PROPFIND/GET/PUT/…)
internal/accounts/       registro multi-cuenta con pool HTTP compartido
internal/oauth/          servidor de recursos OAuth: verificación de tokens OIDC/Nextcloud, mapeo de usuarios
internal/mcpsrv/         cableado del servidor MCP, registro de herramientas, handlers
docs/oauth-*.md          guías de OAuth (proveedor OIDC, Nextcloud)
docs/TESTING.md          estrategia y convenciones de pruebas
test/e2e/                pruebas end-to-end (build tag `e2e`)
```

### Pruebas

La suite es hermética (sin red, sin Nextcloud en vivo) y corre en segundos:

```sh
go test -race ./...
```

La cobertura en los paquetes críticos de seguridad es alta por diseño — ver
[`docs/TESTING.md`](docs/TESTING.md) para la estrategia completa, niveles y las
convenciones que seguimos.

## Contribuir

¡Las contribuciones son bienvenidas! Lee [CONTRIBUTING.md](CONTRIBUTING.md) para conocer
el flujo de desarrollo, las reglas básicas y el proceso de pull requests. Se espera que
todos los participantes sigan nuestro [Código de Conducta](CODE_OF_CONDUCT.md).

### Primeras issues recomendadas

- Añadir una prueba de paginación de `list_files` que afirme `next_offset`.
- Documentar un despliegue real (reverse proxy + streamable-http) en el README.
- Extender la prueba de ciclo completo para cubrir `search_files` con carpetas anidadas
  y profundidad multi-nivel.

## Seguridad

Por favor **no** abras issues públicas por vulnerabilidades de seguridad. Sigue el proceso
de reporte privado descrito en [SECURITY.md](SECURITY.md).

## Gobernanza y soporte

- [MAINTAINERS.md](MAINTAINERS.md) — mantenedores, roles, toma de decisiones y política
  de versiones.
- [SUPPORT.md](SUPPORT.md) — opciones de soporte comunitario y comercial.
- [CHANGELOG.md](CHANGELOG.md) — notas de versión.

## Licencia

Copyright © 2026 Valdrent y los contribuidores de nextcloud-mcp-fast.

Distribuido bajo la [Licencia Apache, versión 2.0](LICENSE). Ver [NOTICE](NOTICE) para la atribución.

Nextcloud es una marca registrada de Nextcloud GmbH. Este proyecto no está afiliado ni
respaldado por Nextcloud GmbH.
