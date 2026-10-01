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
  `streamable-http` (despliegues remotos).
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

## Configuración (variables de entorno)

| Variable | Obligatoria | Por defecto | Descripción |
|---|---|---|---|
| `NEXTCLOUD_HOST` | sí | — | URL base de Nextcloud, p. ej. `https://cloud.example.com` |
| `NEXTCLOUD_USERNAME` | * | — | Nombre de usuario de la cuenta (omitir si el pass-through está activo) |
| `NEXTCLOUD_PASSWORD` | * | — | **App Password** (no tu contraseña de acceso) |
| `NEXTCLOUD_MCP_TRANSPORT` | no | `stdio` | `stdio` o `http` |
| `NEXTCLOUD_MCP_HTTP_ADDR` | no | `127.0.0.1:8000` | Dirección de escucha en modo HTTP (la imagen Docker usa `:8000`) |
| `NEXTCLOUD_MCP_HTTP_TOKEN` | modo http | — | Token Bearer, mínimo 32 caracteres (p. ej. `openssl rand -hex 32`); los clientes envían `Authorization: Bearer <token>` |
| `NEXTCLOUD_MCP_ALLOWED_HOSTS` | no | — | Lista blanca separada por comas de `scheme://host[:puerto]` para pass-through; `NEXTCLOUD_HOST` siempre está permitido; las entradas `http://` solo si el host por defecto es `http://` |
| `NEXTCLOUD_MCP_PERMISSIONS` | no | `read` | `read`, `write` o `destructive` (alias `full`) |
| `NEXTCLOUD_MCP_PASSTHROUGH` | no | `false` | Permite credenciales por solicitud (multi-cuenta); requiere modo `http`. Las credenciales solo llegan por las cabeceras `X-Nextcloud-Host`/`X-Nextcloud-Username`/`X-Nextcloud-Password` |
| `NEXTCLOUD_MCP_MAX_READ_BYTES` | no | `1048576` | Límite para una sola llamada a `read_file` |
| `NEXTCLOUD_MCP_MAX_LIST_ENTRIES` | no | `50` | Entradas por página de `list_files` (máx. 200) |
| `NEXTCLOUD_MCP_HTTP_TIMEOUT` | no | `30s` | Timeout WebDAV por solicitud |
| `NEXTCLOUD_MCP_CB_THRESHOLD` | no | `10` | Llamadas fallidas dentro de la ventana antes de disparar el disyuntor (`0` lo desactiva) |
| `NEXTCLOUD_MCP_CB_WINDOW` | no | `1m` | Duración de la ventana deslizante del disyuntor |

\* Obligatoria salvo que `NEXTCLOUD_MCP_PASSTHROUGH=true`.

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
- La configuración usa la imagen de contenedor publicada. Mientras no exista una
  versión etiquetada, puedes usar `"command": "go"` con
  `"args": ["run", "github.com/valdrent/nextcloud-mcp-fast@main"]`.
- Para verificarlo, abre los logs de una sesión de Copilot y expande **Start MCP Servers**.

## Herramientas

| Herramienta | Permiso | Descripción |
|---|---|---|
| `list_files` | read | Lista un directorio (paginado vía `limit`/`offset`) |
| `read_file` | read | Lee un archivo (texto o base64), con `offset`/`length` para lecturas parciales |
| `write_file` | write | Crea/sobrescribe un archivo; las carpetas padres se crean automáticamente si no existen |
| `create_folder` | write | Crea un directorio |
| `move_file` | write | Renombra/mueve una ruta; `overwrite` para reemplazar un destino existente |
| `delete` | destructive | Elimina un archivo o carpeta — **Nextcloud elimina las carpetas de forma recursiva** (los elementos van a la papelera de Nextcloud si está habilitada) |
| `search_files` | read | Búsqueda de nombres sin distinguir mayúsculas (profundidad acotada) |
| `stat` | read | Metadatos de una única ruta |

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
- **El modo HTTP no tiene autenticación propia.** Cualquiera que alcance el endpoint
  `/mcp` puede actuar con la cuenta configurada. Enlázalo a `127.0.0.1` (como hace el
  archivo compose) o ponlo detrás de un reverse proxy que exija TLS y autenticación.
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
internal/mcpsrv/         cableado del servidor MCP, registro de herramientas, handlers
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
