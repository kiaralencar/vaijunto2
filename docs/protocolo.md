# Protocolo VaiJunto (cliente ↔ servidor da própria empresa)

Protocolo de aplicação próprio, sobre socket TCP puro (pacote `net` do Go).
Sem HTTP, sem framework de mensageria ou RPC.

É o mesmo protocolo do Problema 1: os clientes motorista e passageiro foram
reaproveitados e continuam conectando **somente no servidor da sua própria
empresa**, sem saber que existem outras. A comunicação **entre os servidores**
das empresas é feita por uma API REST separada, especificada em
[`api-rest.md`](api-rest.md).

O que mudou em relação ao Problema 1:

- IDs passaram a ser únicos no consórcio: caronas são `"<empresa>-C<n>"`
  (ex.: `"vaijunto-C1"`) e reservas `"<empresa>-R<n>"` (ex.: `"rotasul-R7"`).
- Itens de itinerário e caronas listadas trazem o campo `empresa`.
- `LISTAR_CARONAS` e `BUSCAR_ITINERARIOS` passam a enxergar as caronas de
  todo o consórcio (o servidor consulta os vizinhos por trás).
- `RESERVAR` passa a coordenar a reserva em todas as empresas envolvidas.

## Por que TCP, e não UDP

O enunciado já direciona para o "subsistema de rede TCP/IP", mas a escolha
entre TCP e UDP dentro dele tem uma justificativa técnica própria, não é só
seguir a letra do enunciado:

- **Confiabilidade e ordem importam aqui.** Uma operação como `RESERVAR`
  precisa chegar inteira, sem corromper, e — quando o mesmo cliente manda
  várias operações em sequência (ex.: `LOGIN` depois `RESERVAR`) — **na
  ordem** em que foram enviadas. TCP garante as duas coisas nativamente
  (entrega confiável, ordenada, sem duplicação). Com UDP, um datagrama pode
  chegar fora de ordem, duplicado ou simplesmente nunca chegar, e cabe à
  aplicação detectar e corrigir isso.
- **Reimplementar isso seria retrabalho sem benefício.** Usar UDP exigiria
  um esquema próprio de confirmação (ACK), retransmissão e reordenação —
  exatamente o que o TCP já resolve na camada de transporte. Para um
  protocolo de requisição-resposta como este (pedido → resposta, sequencial,
  na mesma conexão), isso não traria vantagem nenhuma, só complexidade.
- **UDP se justificaria** em cenários com tolerância a perda e sensibilidade a
  atraso (streaming de vídeo, jogos em tempo real) — não é o caso de reservar
  assento, onde perder uma mensagem silenciosamente seria um bug grave, não
  um detalhe aceitável.
- **Conexão persistente também ajuda neste modelo**: como cada cliente
  mantém uma conexão TCP aberta durante toda a sessão (múltiplas operações,
  uma mesma conexão), o custo de estabelecer a conexão (handshake do TCP)
  acontece uma vez só, no login — não a cada operação.

## Transporte e enquadramento

TCP entrega um fluxo contínuo de bytes — não sabe onde uma mensagem termina e
a próxima começa. Quem define isso é este protocolo:

- Cada mensagem é um objeto **JSON compacto** (sem quebras de linha internas).
- Cada mensagem termina com **`\n`**.
- O receptor lê até encontrar `\n` (`bufio.NewReader(conn).ReadString('\n')`)
  e considera aquilo uma mensagem completa.

Essa escolha (JSON Lines) foi preferida a um prefixo de tamanho por ser mais
simples de depurar — dá para inspecionar o tráfego com `netcat`/`telnet`.

## Fluxo de conexão

1. Cliente conecta com `net.Dial("tcp", "host:porta")`.
2. Servidor aceita a conexão (`Listener.Accept()`) e entrega a uma goroutine
   dedicada — a conexão permanece **aberta** durante toda a sessão.
3. Primeira operação esperada do cliente: `LOGIN`.
4. Depois do login, o cliente manda quantas operações quiser, na ordem que
   quiser, todas na mesma conexão (um pedido, uma resposta, cada vez).
5. Desconexão: o cliente fecha o socket, ou cai. O servidor detecta erro na
   leitura, encerra **só aquela goroutine**. O estado do servidor e as demais
   conexões não são afetados.
6. Mensagem malformada (JSON inválido, campo faltando): o servidor responde
   com `"ok": false` e um `erro`, e continua atendendo normalmente na mesma
   conexão — nunca derruba o servidor.

## Envelope

Todo pedido (cliente → servidor):

```json
{"tipo": "NOME_DA_OPERACAO", "id": "opcional", "dados": { ... }}
```

Toda resposta (servidor → cliente):

```json
{"tipo": "NOME_DA_OPERACAO", "id": "opcional", "ok": true, "erro": "", "dados": { ... }}
```

- `id` é opcional e apenas ecoado de volta — reservado para um cliente futuro
  que precise casar pedidos e respostas fora de ordem.
- Quando `ok` é `false`, `erro` traz uma mensagem legível para humano e
  `dados` vem vazio.

## Operações

| Operação | Quem usa | Requer login |
|---|---|---|
| `LOGIN` | motorista e passageiro | — |
| `PUBLICAR_CARONA` | motorista | sim |
| `LISTAR_CARONAS` | motorista e passageiro | não |
| `CONSULTAR_PASSAGEIROS` | motorista | sim, e só o dono da carona |
| `CANCELAR_CARONA` | motorista | sim |
| `BUSCAR_ITINERARIOS` | passageiro | não |
| `RESERVAR` | passageiro | sim |
| `LISTAR_MINHAS_RESERVAS` | passageiro | sim |
| `CANCELAR_RESERVA` | passageiro | sim |

### `LOGIN`

Pedido `dados`: `{"nome": "Maria", "senha": "qualquercoisa"}`
Resposta `dados`: `{}`

Efeito: associa o nome informado a esta conexão (sessão). A senha não é um
sistema de conta completo (sem hashing) — serve só para evitar que dois
clientes diferentes colidam usando o mesmo nome sem querer: quem usa um nome
pela primeira vez "registra" a senha para ele; logins seguintes com o mesmo
nome precisam vir com a mesma senha, senão são recusados. Os nomes e senhas
ficam no armazenamento da própria empresa: cada empresa tem os seus usuários,
e um passageiro não precisa de conta nas outras.

### `PUBLICAR_CARONA`

Pedido `dados`:
```json
{
  "rota": ["Salvador", "Vitoria da Conquista"],
  "data": "20/09/2026",
  "preco_por_trecho": [50],
  "assentos_por_trecho": [2]
}
```
Resposta `dados`: `{"carona_id": "vaijunto-C1"}`

Regra: `preco_por_trecho` e `assentos_por_trecho` têm exatamente um valor por
trecho, isto é, `len(rota) - 1` valores.

Convenção de data: `DD/MM/AAAA` (ex.: `"20/09/2026"`). O campo `data` é
tratado como texto — o servidor só faz comparação exata (`BUSCAR_ITINERARIOS`)
e conversão para ordenar `LISTAR_CARONAS` por data mais próxima; ele **não**
valida se a data é real (ex.: "31/02/2026" seria aceito sem erro).

### `LISTAR_CARONAS`

Pedido `dados`: `{}`
Resposta `dados`:
```json
{
  "caronas": [
    {
      "id": "vaijunto-C1", "empresa": "vaijunto", "motorista": "Joao",
      "rota": ["Salvador", "Vitoria da Conquista"],
      "data": "20/09/2026",
      "preco_por_trecho": [50],
      "assentos_livres": [2]
    }
  ]
}
```

Lista as caronas ativas de **todo o consórcio**: as da própria empresa e as
de cada vizinho que estiver no ar. O nome do motorista só aparece nas caronas
da própria empresa — as outras empresas não enviam esse dado (privacidade).

### `CONSULTAR_PASSAGEIROS`

Pedido `dados`: `{"carona_id": "vaijunto-C1"}`
Resposta `dados`:
```json
{"passageiros": [{"passageiro": "Maria (rotasul)", "trecho_inicio": 0, "trecho_fim": 0}]}
```

Passageiros de outras empresas aparecem com a empresa entre parênteses.

Exige login, e **só o motorista dono da carona** pode consultar — outro
motorista pedindo o mesmo `carona_id` recebe erro ("carona pertence a outro
motorista"), mesmo estando autenticado.

### `CANCELAR_CARONA`

Pedido `dados`: `{"carona_id": "vaijunto-C1"}`
Resposta `dados`: `{}`

Só o motorista dono da carona pode cancelar. Cancelar uma carona também
cancela qualquer reserva que dependesse dela — inclusive reservas de
itinerário combinado com caronas de outras empresas, já que o itinerário como
um todo deixa de poder ser cumprido. A empresa da carona avisa o coordenador
de cada reserva afetada (`POST /reservas/{id}/invalidacao`), e ele cancela a
reserva em todas as empresas, devolvendo os assentos.

### `BUSCAR_ITINERARIOS`

Pedido `dados`: `{"origem": "Salvador", "destino": "Vitoria da Conquista", "data": "20/09/2026"}`
Resposta `dados`:
```json
{
  "itinerarios": [
    {
      "itens": [{
        "empresa": "vaijunto", "carona_id": "vaijunto-C1", "trecho_inicio": 0, "trecho_fim": 0,
        "origem": "Salvador", "destino": "Vitoria da Conquista", "preco": 50
      }],
      "preco": 50
    }
  ]
}
```
`origem`, `destino` e `preco` dentro de cada item são só para exibição
(o servidor busca o nome das cidades na carona correspondente) — o cliente
não precisa preenchê-los para reservar, só repassar o item inteiro de volta
num `RESERVAR`.

**Atenção — comparação de cidade e data é texto exato**: `"Salvador"` e
`"salvador"` são valores diferentes (maiúscula importa), assim como
`"Vitoria da Conquista"` e `"Vitória da Conquista"` (acento importa). O
mesmo vale para a data. Não há normalização hoje — é preciso digitar
exatamente igual ao que foi usado em `PUBLICAR_CARONA`.

Como é feito: o servidor junta as suas caronas daquela data com as de cada
vizinho (`GET /caronas?data=...`, consultados em paralelo, com timeout — um
vizinho fora do ar é ignorado e a busca continua com os demais) e monta um
**grafo** a partir dessas caronas — cada **cidade** é um nó, cada **trecho com assento livre** é
uma **aresta** (marcada com a carona dona do trecho e o preço). Uma busca em
profundidade (DFS) de `origem` a `destino`, sem revisitar cidade, encontra os
caminhos possíveis; trechos consecutivos da mesma carona são agrupados num
único item de reserva. A busca é limitada a 6 trechos e 10 itinerários por
consulta, para não explodir combinatoriamente.

Critério de ordenação da lista devolvida: **preço total, do mais barato para
o mais caro**; em caso de empate, o itinerário com **menos itens** (menos
trocas de motorista) vem primeiro, por ser mais simples de executar na
prática. Sem essa ordenação explícita, a lista sairia numa ordem
não-determinística — mapas em Go são iterados em ordem aleatória de
propósito — o que seria uma falha de projeto, não só um detalhe estético.

### `RESERVAR`

Pedido `dados`:
```json
{"itens": [{"empresa": "rotasul", "carona_id": "rotasul-C1", "trecho_inicio": 0, "trecho_fim": 0}]}
```
Resposta `dados`: `{"reserva_id": "vaijunto-R1"}`

`empresa` é opcional: se vier vazio, o servidor a deduz pelo ID da carona.

Regra central do sistema: **todos os itens são confirmados atomicamente** —
ou entram todos, ou nenhum —, mesmo quando pertencem a empresas diferentes. O
servidor do passageiro coordena a reserva em duas fases com as empresas donas
dos trechos (preparar e depois confirmar ou abortar), como descrito em
[`api-rest.md`](api-rest.md). A resposta só chega ao cliente depois da decisão.
Se a reserva for recusada, `erro` explica o motivo (sem assento, empresa
indisponível, reserva com precedência levou o último assento...).
Um `ReservarReq` pode conter itens de caronas diferentes (itinerário
combinado) ou vários trechos consecutivos da mesma carona; é a mesma
operação nos dois casos. O cliente passageiro reserva sempre a partir de um
resultado do `BUSCAR_ITINERARIOS`, repassando os itens do itinerário escolhido.

### `LISTAR_MINHAS_RESERVAS`

Pedido `dados`: `{}`
Resposta `dados`:
```json
{
  "reservas": [{
    "id": "vaijunto-R1",
    "itens": [{
      "empresa": "rotasul", "carona_id": "rotasul-C1", "trecho_inicio": 0, "trecho_fim": 0,
      "origem": "Salvador", "destino": "Vitoria da Conquista", "preco": 50
    }]
  }]
}
```
Assim como em `BUSCAR_ITINERARIOS`, `origem`/`destino`/`preco` são
preenchidos pelo servidor só para exibição.

### `CANCELAR_RESERVA`

Pedido `dados`: `{"reserva_id": "vaijunto-R1"}`
Resposta `dados`: `{}`

Devolve o(s) assento(s) em cada trecho da reserva cancelada, em todas as
empresas envolvidas (o coordenador avisa cada uma).

## Exemplo de troca real (capturado rodando o sistema)

```
cliente-motorista -> servidor:
{"tipo":"LOGIN","dados":{"nome":"Motorista1"}}

servidor -> cliente-motorista:
{"tipo":"LOGIN","ok":true,"dados":{}}

cliente-motorista -> servidor:
{"tipo":"PUBLICAR_CARONA","dados":{"rota":["Salvador","Vitoria da Conquista"],"data":"20/09/2026","preco_por_trecho":[50],"assentos_por_trecho":[2]}}

servidor -> cliente-motorista:
{"tipo":"PUBLICAR_CARONA","ok":true,"dados":{"carona_id":"vaijunto-C1"}}

cliente-passageiro -> servidor:
{"tipo":"LOGIN","dados":{"nome":"Passageiro1"}}

servidor -> cliente-passageiro:
{"tipo":"LOGIN","ok":true,"dados":{}}

cliente-passageiro -> servidor:
{"tipo":"RESERVAR","dados":{"itens":[{"carona_id":"vaijunto-C1","trecho_inicio":0,"trecho_fim":0}]}}

servidor -> cliente-passageiro:
{"tipo":"RESERVAR","ok":true,"dados":{"reserva_id":"vaijunto-R1"}}
```

## Concorrência (resumo — detalhes no relatório)

Dentro de cada empresa, todo o estado (caronas, reservas, participações,
senhas) vive atrás de **um único `sync.Mutex`**, como no Problema 1. Entre
empresas, a coordenação é feita pelo protocolo da API REST (ver
[`api-rest.md`](api-rest.md)): nenhuma chamada de rede é feita com o mutex
travado, para que uma empresa lenta nunca bloqueie as operações locais.

## Confiabilidade

**Queda abrupta de cliente**: cada conexão roda numa goroutine própria
(`atendeCliente`, em `main.go`). Um erro de leitura nessa goroutine — cliente
fechou o socket, caiu a rede, ou o timeout abaixo disparou — encerra **só
aquela goroutine**. O estado do servidor e as demais conexões continuam
intactos.

**Timeout de socket**: cada leitura tem um prazo (`conn.SetReadDeadline`,
reiniciado a cada mensagem recebida) de 1 hora de inatividade. Se um cliente
conectar e nunca mandar nada (ou parar de responder no meio de uma sessão), a
conexão é fechada pelo servidor depois desse prazo.

**Queda do cliente no meio de uma reserva**: a reserva continua sendo
decidida pelo servidor normalmente; o resultado fica em
`LISTAR_MINHAS_RESERVAS` na próxima conexão. Diferente do Problema 1, agora
existe o estado intermediário "assento segurado aguardando decisão" — mas
ele pertence ao protocolo entre servidores, que garante que esse estado
sempre termina (ver "Falhas e recuperação" em [`api-rest.md`](api-rest.md)).
