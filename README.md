# VAIJUNTO — Consórcio de Caronas Compartilhadas

## Contexto acadêmico

Este projeto foi desenvolvido no contexto da disciplina MI — Concorrência e
Conectividade, do curso de Engenharia de Computação da Universidade Estadual
de Feira de Santana (UEFS), como trabalho individual dentro de um problema
baseado em aprendizagem (PBL). É o segundo problema da disciplina e evolui o
sistema construído no [Problema 1](https://github.com/kiaralencar/vaijunto).

## Sobre o projeto

No Problema 1, o VAIJUNTO era um sistema de caronas compartilhadas com um
único servidor central, que guardava todas as caronas e reservas. Motoristas
publicavam caronas com rota e preço por trecho, e passageiros reservavam
itinerários que podiam combinar trechos de caronas de motoristas diferentes.

Neste problema, o VAIJUNTO e outras empresas regionais de caronas formam um
**consórcio**. Cada empresa continua operando o seu próprio servidor, com os
seus motoristas, passageiros e armazenamento, e nenhuma aceita depender de um
servidor central ou entregar os seus dados a outra. Mesmo assim, um passageiro
de qualquer empresa deve conseguir montar uma viagem combinando caronas de
todas elas, sem criar conta em cada uma. Por exemplo, um passageiro do
VAIJUNTO pode ir de Salvador a Montes Claros com o trecho Salvador → Feira de
Santana numa carona do VAIJUNTO, o trecho Feira de Santana → Vitória da
Conquista numa carona de uma segunda empresa e o trecho Vitória da Conquista →
Montes Claros numa carona de uma terceira.

No Problema 1, uma trava dentro do servidor central bastava para garantir que
um itinerário fosse reservado por completo ou não fosse reservado. Agora os
assentos de um mesmo itinerário pertencem a servidores diferentes, cada um com
a sua memória, o seu relógio e a sua fila de requisições, e nenhuma trava
local protege o itinerário inteiro. O sistema precisa então coordenar os
servidores entre si, sem um coordenador central, e continuar correto quando
um deles cai.

### Requisitos do problema e o que foi implementado

| Requisito do enunciado | Solução |
|---|---|
| Confirmação atômica do itinerário, mesmo com trechos de empresas diferentes | *Two-phase commit* implementado no próprio servidor: o servidor da empresa do passageiro coordena a reserva; cada empresa dona de um trecho primeiro segura o assento e depois confirma ou desfaz, conforme a decisão do coordenador |
| Mesmo assento nunca confirmado para dois passageiros, mesmo com pedidos simultâneos vindos de empresas diferentes | Só a empresa dona da carona altera os assentos dela, sob o seu mutex |
| Critério de precedência na disputa pelo último assento, com relógios não sincronizados e mensagens que atrasam | Cada reserva recebe um timestamp do **relógio lógico de Lamport**; o menor tem precedência e o empate é decidido pelo ID da empresa. Uma reserva com precedência pode tomar o assento de uma mais nova que **ainda não foi confirmada**; a mais nova nunca espera, o que evita deadlock |
| Queda de servidor, inclusive no meio de uma reserva, sem itinerários pela metade nem assentos travados; estado consistente após a recuperação | Cada empresa grava o seu estado em disco antes de responder; assento segurado tem prazo para a decisão chegar; o participante pergunta a decisão ao coordenador; ao reiniciar, o coordenador aborta as reservas que ficaram sem decisão; as mensagens são reenviadas até serem entregues |
| Queda detectada por timeout, sem erro fatal para as demais empresas | Timeout de 2 s em toda requisição entre servidores e verificação periódica do estado dos vizinhos (*heartbeat*) |
| Nenhuma solução centralizada | Não há servidor central, coordenador fixo nem armazenamento compartilhado: cada reserva é coordenada pelo servidor do próprio passageiro, e cada servidor guarda só os dados da sua empresa |
| Comunicação entre servidores por API REST | API REST sobre HTTP, implementada com a biblioteca padrão do Go |
| Coordenação distribuída implementada pela aplicação | Exclusão mútua, atomicidade e recuperação de falhas implementadas no próprio código, sem serviços de coordenação, bancos de dados distribuídos ou gerenciadores de transação |
| Clientes motorista e passageiro conectando só no servidor da própria empresa | Clientes do Problema 1 reaproveitados: o servidor da empresa do passageiro consulta as outras por trás, e o cliente não sabe que elas existem |
| Simulador de passageiros e teste automatizado com carga e injeção de falhas | Simulador sem interface gráfica e teste que derruba servidores no meio da carga, descritos nas seções [Simulador](#simulador-de-passageiros) e [Testes](#testes) |

Algumas decisões vão além do que o enunciado exige literalmente e vale
deixá-las claras:

- **Persistência em disco.** No Problema 1 todo o estado vivia só na memória.
  Aqui, cada empresa grava cada alteração num arquivo próprio, de forma
  atômica, antes de responder. Isso é o que permite a uma empresa que caiu no
  meio de uma reserva voltar com o estado consistente.
- **A API entre servidores não expõe o nome do motorista.** As outras empresas
  recebem só o necessário para montar itinerários (rota, data, preço e
  assentos livres), em respeito à privacidade dos usuários citada no enunciado.
- **Algumas ideias discutidas nas sessões tutoriais não foram adotadas.**
  Replicar os trechos de cada empresa nos outros servidores contraria o
  enunciado, que exige que cada servidor guarde apenas os dados da sua
  empresa; a consistência entre os grafos vem de consultar os vizinhos no
  momento da busca. Uma eleição de coordenador não é necessária, porque não
  existe coordenador fixo. O algoritmo de Ricart-Agrawala não foi usado:
  cada assento tem uma única empresa dona, que já serializa o acesso a ele.

## Funcionalidades principais

**Cliente motorista**
- Autenticar-se com nome e senha na sua empresa.
- Publicar uma carona, informando rota (sequência de cidades), data, preço e
  quantidade de assentos por trecho.
- Listar as caronas ativas de todo o consórcio.
- Consultar os passageiros confirmados numa carona própria, por trecho,
  inclusive passageiros de outras empresas.
- Cancelar uma carona publicada (cancela também, em todas as empresas, as
  reservas que dependiam dela).

**Cliente passageiro**
- Autenticar-se com nome e senha na sua empresa.
- Listar as caronas ativas de todo o consórcio.
- Buscar itinerários entre uma origem e um destino numa data, incluindo
  itinerários que combinam caronas de empresas diferentes, e reservar um dos
  resultados encontrados.
- Listar as próprias reservas.
- Cancelar uma reserva (os assentos são devolvidos em todas as empresas
  envolvidas).

## Como funciona

O sistema é formado por quatro programas Go, dentro de um único módulo:

- **Servidor de empresa** (`cmd/servidor`) — o mesmo programa roda uma vez
  para cada empresa do consórcio; o que define qual empresa ele é são as
  flags de inicialização. Cada instância tem duas portas:
  - **TCP (8080)**, para os clientes motorista e passageiro, com o protocolo
    do Problema 1;
  - **HTTP (9090)**, com a API REST usada **só** pelos servidores das outras
    empresas.
- **Cliente motorista** e **cliente passageiro** (`cmd/cliente-motorista`,
  `cmd/cliente-passageiro`) — programas de terminal reaproveitados do
  Problema 1, que se conectam somente ao servidor da sua empresa.
- **Simulador de passageiros** (`cmd/simulador`) — gera carga sem interface
  gráfica.

```
  motorista / passageiro            motorista / passageiro
          │ TCP (protocolo do P1)            │
          ▼                                  ▼
  ┌───────────────┐   API REST (HTTP)  ┌───────────────┐
  │   vaijunto    │◄──────────────────►│    rotasul    │
  │ :8080  :9090  │                    │ :8080  :9090  │
  └───────┬───────┘                    └───────┬───────┘
          │            API REST (HTTP)         │
          └──────────────►┌───────────────┐◄───┘
                          │ caminhonorte  │
                          │ :8080  :9090  │
                          └───────────────┘
```

Alguns pontos de funcionamento relevantes:

- **Vizinhos.** Cada servidor conhece os endereços das empresas parceiras pela
  flag `-vizinhos`, já que não existe um diretório central onde elas possam se
  descobrir. Com três empresas, cada uma lista as outras duas (malha completa).
- **Identificadores únicos no consórcio.** Caronas são identificadas como
  `<empresa>-C<n>` (ex.: `vaijunto-C1`) e reservas como `<empresa>-R<n>` (ex.:
  `rotasul-R7`), de modo que o próprio ID diz a qual empresa pertencem.
- **Busca distribuída.** O servidor do passageiro junta as suas caronas da data
  pedida com as de cada vizinho, consultados em paralelo, e monta o mesmo grafo
  do Problema 1 (cada cidade é um nó, cada trecho com assento livre é uma
  aresta), com uma busca em profundidade entre a origem e o destino. Um vizinho
  fora do ar é ignorado e a busca continua com as empresas disponíveis.
- **Reserva em duas fases.** Na primeira fase, o servidor do passageiro pede a
  cada empresa envolvida que segure os assentos dos seus trechos. Só se todas
  aceitarem, ele grava a reserva como confirmada e pede a todas que confirmem;
  se qualquer uma recusar ou não responder, grava a reserva como abortada e
  pede a todas que devolvam os assentos.
- **Concorrência dentro de cada empresa.** Como no Problema 1, cada cliente é
  atendido na sua própria goroutine, e todo o estado da empresa é protegido
  por um único `sync.Mutex`. Nenhuma requisição a outra empresa é feita com o
  mutex travado, para que uma empresa lenta nunca bloqueie as operações locais.
- **Recuperação.** Ao reiniciar, a empresa recarrega o seu estado do disco,
  aborta as reservas que coordenava e que ficaram sem decisão, e retoma o
  envio das decisões que ainda não tinham chegado às outras empresas.
- **Conexões de clientes** ociosas por mais de 1 hora são encerradas pelo
  servidor; a queda de um cliente encerra apenas a goroutine daquela conexão.

## Protocolos

- **Entre servidores:** API REST sobre HTTP, com corpo em JSON e o relógio de
  Lamport num cabeçalho de toda mensagem. A especificação completa — recursos,
  métodos, formato das requisições e respostas, códigos de retorno e os
  diagramas de sequência da confirmação de um itinerário com trechos de
  empresas diferentes, da disputa pelo último assento e da recuperação de
  falhas — está em [`docs/api-rest.md`](docs/api-rest.md).
- **Entre cliente e servidor:** o protocolo próprio do Problema 1, sobre socket
  TCP puro, com uma mensagem JSON por linha. A especificação está em
  [`docs/protocolo.md`](docs/protocolo.md).

## Tecnologias utilizadas

- **Go** (módulo `vaijunto`, `go 1.23` no `go.mod`) — sem dependências
  externas, apenas biblioteca padrão (`net`, `net/http`, `encoding/json`,
  `sync`, `flag`, `os`, entre outras).
- **Docker** e **Docker Compose** — para executar cada servidor de empresa em
  um contêiner isolado.

## Estrutura do projeto

```
cmd/
  servidor/
    main.go                ponto de entrada e atendimento dos clientes TCP
    config.go              flags de inicialização
    handlers.go            operações do protocolo dos clientes
    api.go                 API REST entre servidores
    servidor.go            coordenação das reservas (papéis de coordenador e participante)
    estado.go              estado da empresa e suas operações, protegidos por mutex
    busca.go               busca distribuída de itinerários por grafo
    vizinhos.go            vizinhos, heartbeat e requisições HTTP com timeout
    lamport.go             relógio lógico de Lamport e critério de precedência
    persistencia.go        gravação atômica do estado em disco
    consorcio_test.go      teste com carga e injeção de falhas
    precedencia_test.go    testes do critério de precedência
  cliente-motorista/
    main.go                cliente de terminal do motorista
  cliente-passageiro/
    main.go                cliente de terminal do passageiro
  simulador/
    main.go                simulador de passageiros
internal/
  dominio/
    dominio.go             entidades (Carona, Reserva, Participacao, ItemReserva)
  protocolo/
    protocolo.go           mensagens do protocolo dos clientes
  cliente/
    cliente.go             cliente TCP mínimo usado pelo simulador e pelos testes
docker/
  servidor.Dockerfile
  cliente-motorista.Dockerfile
  cliente-passageiro.Dockerfile
  simulador.Dockerfile
docker-compose.yml         três empresas, clientes e simulador numa única máquina
docs/
  api-rest.md              especificação da API REST entre servidores
  protocolo.md             especificação do protocolo dos clientes
  diagrama2.pdf            diagrama de sequência do problema
  imagens/                 capturas dos testes realizados
  referencias.txt          referências utilizadas
go.mod
```

## Pré-requisitos

- Go 1.23 ou superior, para execução nativa ou para rodar os testes.
- Docker e Docker Compose, para execução em contêineres.

O projeto não tem dependências externas de terceiros — não é necessário rodar
`go mod download` ou similar.

## Instalação

```
git clone https://github.com/kiaralencar/vaijunto2.git
cd vaijunto2
```

## Configuração do servidor

Cada instância do servidor é configurada por flags (`-h` lista todas):

| Flag | Padrão | Significado |
|---|---|---|
| `-empresa` | (obrigatória) | ID da empresa no consórcio |
| `-porta-tcp` | `8080` | Porta dos clientes motorista e passageiro |
| `-porta-http` | `9090` | Porta da API REST entre servidores |
| `-vizinhos` | (nenhum) | Endereços HTTP das outras empresas, separados por vírgula |
| `-dados` | `dados` | Pasta onde o estado é gravado (uma subpasta por empresa); vazio desliga a gravação |
| `-prazo-decisao` | `10s` | Tempo que um assento fica segurado esperando a decisão do coordenador |
| `-atraso-decisao` | `0` | Atraso entre as duas fases da reserva, usado apenas nos testes de falha |

## Execução nativa

Os clientes conectam no servidor da sua empresa, então os servidores precisam
estar rodando antes. Em três terminais, a partir da raiz do repositório, com as
três empresas na mesma máquina (por isso cada uma usa portas diferentes):

```
go run ./cmd/servidor -empresa=vaijunto     -porta-tcp=8080 -porta-http=9090 -vizinhos=http://localhost:9091,http://localhost:9092
go run ./cmd/servidor -empresa=rotasul      -porta-tcp=8081 -porta-http=9091 -vizinhos=http://localhost:9090,http://localhost:9092
go run ./cmd/servidor -empresa=caminhonorte -porta-tcp=8082 -porta-http=9092 -vizinhos=http://localhost:9090,http://localhost:9091
```

Em outros terminais, cada cliente recebe o endereço do servidor da sua empresa:

```
go run ./cmd/cliente-motorista  127.0.0.1:8081
go run ./cmd/cliente-passageiro 127.0.0.1:8080
```

## Execução com Docker

### Numa máquina só

O `docker-compose.yml` sobe as três empresas, cada uma no seu contêiner, com os
vizinhos já configurados e um volume de dados próprio:

```
docker compose up --build -d vaijunto rotasul caminhonorte
docker compose logs -f
```

O segundo comando acompanha ao vivo o que cada servidor imprime (Ctrl+C sai da
visualização sem parar os servidores). Em outros terminais, os clientes
conectam pelo nome do serviço:

```
docker compose run --rm cliente-motorista rotasul:8080
docker compose run --rm cliente-passageiro vaijunto:8080
```

Para parar tudo e apagar os dados das empresas:

```
docker compose down -v
```

### Em máquinas distintas

Em cada máquina, uma empresa, informando os IPs das outras como vizinhos:

```
docker build -f docker/servidor.Dockerfile -t vaijunto-servidor .
docker run -p 8080:8080 -p 9090:9090 -v dados:/dados vaijunto-servidor -empresa=vaijunto -vizinhos=http://<IP-2>:9090,http://<IP-3>:9090
```

As outras máquinas usam o mesmo comando, trocando `-empresa` e a lista de
vizinhos. O IP de cada máquina pode ser obtido com `hostname -I`. Os clientes
conectam no IP da máquina da sua empresa, na porta 8080:

```
docker build -f docker/cliente-passageiro.Dockerfile -t vaijunto-cliente-passageiro .
docker run -it --rm vaijunto-cliente-passageiro <IP-DA-EMPRESA>:8080
```

## Manual de uso

Depois de conectado, cada cliente pede nome e senha. Se o nome ainda não foi
usado naquela empresa, a senha informada passa a valer para ele; se já existe,
a senha precisa bater com a da primeira vez.

Fluxo típico, montando um itinerário com caronas de duas empresas:

**No cliente motorista da rotasul**, publicar uma carona (opção 1 do menu):

```
Rota: Feira de Santana, Vitoria da Conquista
Data (formato DD/MM/AAAA): 20/11/2026

Trecho 0: Feira de Santana -> Vitoria da Conquista
Valor: 60
Quantidade de assentos: 2
```

O servidor responde com o ID da carona criada (ex.: `rotasul-C1`). Do mesmo
modo, um motorista da vaijunto publica uma carona `Salvador, Feira de Santana`.

**No cliente passageiro da vaijunto**, buscar um itinerário e reservar
(opção 2 do menu):

```
Origem: Salvador
Destino: Vitoria da Conquista
Data (formato DD/MM/AAAA): 20/11/2026
```

A busca retorna os itinerários encontrados, com o preço total e os trechos que
os compõem, cada um com a empresa dona da carona. Escolher um pelo número o
reserva; a reserva é confirmada nas duas empresas ou em nenhuma.

O motorista pode consultar quem reservou a sua carona (opção 3 do menu do
motorista) e cancelá-la (opção 4). O passageiro pode listar as suas reservas
(opção 3 do menu do passageiro) e cancelá-las (opção 4). Digitar `0` em
qualquer menu volta à tela de login, sem encerrar o programa.

## Simulador de passageiros

O simulador conecta vários passageiros ao mesmo tempo nos servidores de todas
as empresas e gera, sem interface gráfica, buscas, reservas e cancelamentos
durante o tempo pedido. A cada 2 s mostra uma linha com o andamento (buscas,
reservas confirmadas e recusadas, cancelamentos e quedas de conexão) e, no
final, o total de cada resultado, a vazão e os tempos de resposta (média, p50,
p95 e máximo).

Com a opção `-semear`, ele antes publica caronas em cadeia entre as empresas
(Salvador → Feira de Santana na primeira, Feira de Santana → Vitória da
Conquista na segunda, Vitória da Conquista → Montes Claros na terceira), para
que existam itinerários que só se completam combinando as três.

```
go run ./cmd/simulador -servidores=127.0.0.1:8080,127.0.0.1:8081,127.0.0.1:8082 -passageiros=100 -duracao=30s -semear
```

Com Docker:

```
docker compose run --rm simulador
```

Opções: `-passageiros`, `-duracao`, `-data`, `-caronas` e `-assentos` (por
empresa, ao semear), `-cancelar` (probabilidade de cancelar uma reserva
confirmada) e `-progresso` (intervalo das linhas de andamento).

## Testes

Os testes ficam em `cmd/servidor`:

- `consorcio_test.go` compila o servidor e sobe cada empresa como um **processo
  separado**. Derrubar um processo (`kill`) equivale a uma queda abrupta do
  servidor: nada é encerrado de forma organizada.
  - **Carga com queda:** três empresas e 60 passageiros disputando itinerários
    que só existem combinando as três; uma das empresas é derrubada no meio da
    carga e religada com os mesmos dados. Ao final, o estado de todas as
    empresas é conferido: nenhum assento confirmado para mais de um
    passageiro, nenhum itinerário confirmado pela metade e nenhum assento
    bloqueado.
  - **Queda do coordenador** depois de a outra empresa segurar o assento e
    antes da decisão: a outra empresa não decide sozinha e, quando o
    coordenador volta, a reserva é abortada e o assento é liberado.
  - **Queda do participante** depois de aceitar a reserva: a reserva é
    confirmada e o participante recebe a confirmação quando volta.
- `precedencia_test.go` verifica o critério de Lamport com desempate, que a
  reserva com precedência toma o assento de uma mais nova ainda não
  confirmada, que um assento confirmado nunca é tomado e que um pedido de
  reserva atrasado, que chega depois do aborto, é recusado.

Para rodar:

```
go test ./... -v
```

Com o detector de condições de corrida do Go (exige compilador C de 64 bits;
funciona sem configuração adicional na maioria dos ambientes Linux):

```
go test ./... -race -v
```

Ou, em qualquer sistema, dentro de um contêiner:

```
docker run --rm -v "${PWD}:/src" -w /src golang:1.23 go test -race ./... -v
```
