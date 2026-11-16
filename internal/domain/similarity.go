package domain

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Búsqueda por similitud de texto, sin servicios externos: TF-IDF y coseno sobre las palabras de los tickets.
// Alcanza para sugerir errores conocidos mientras alguien escribe un incidente y para detectar incidentes parecidos.

// Palabras que no aportan nada para comparar tickets.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`que los las del por con una para como pero mas sus les este esta esto estos estas
		ese esa eso hay ser son fue era muy sin sobre entre cuando todo todos toda todas nos ya otra otro otros tambien
		desde hasta donde porque cada mismo misma cual les fueron puede pueden tiene tienen hace hacer anda andan
		funciona funcionan problema problemas error errores incidente incidentes favor hola gracias usuario usuarios`) {
		stopwords[w] = true
	}
}

var unaccent = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n")

// Terms normaliza un texto a sus palabras comparables: minúsculas, sin acentos, sin palabras vacías
// y con un plural simple quitado (impresoras -> impresora), así "la impresora" y "las impresoras" coinciden.
func Terms(text string) []string {
	text = unaccent.Replace(strings.ToLower(text))
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := []string{}
	for _, w := range words {
		if len([]rune(w)) < 3 || stopwords[w] {
			continue
		}
		if len(w) > 4 && strings.HasSuffix(w, "s") {
			w = strings.TrimSuffix(w, "s")
		}
		out = append(out, w)
	}
	return out
}

// Match: un documento parecido al texto buscado. Score va de 0 a 1; Shared son las palabras en común,
// para que quien lo ve entienda por qué se sugirió.
type Match struct {
	ID     int      `json:"id"`
	Score  float64  `json:"score"`
	Shared []string `json:"shared"`
}

// MinMatchScore: por debajo de esto la coincidencia es casualidad (una palabra suelta).
const MinMatchScore = 0.18

// rank compara query contra docs (id -> texto) y devuelve los mejores limit por encima de MinMatchScore.
// bonus suma puntaje a los documentos que el llamador considera más cercanos (por ejemplo, el mismo servicio).
// Hace falta compartir al menos dos palabras, o una si el documento tiene bonus.
func rank(query string, docs map[int]string, bonus map[int]float64, limit int) []Match {
	q := Terms(query)
	if len(q) == 0 || len(docs) == 0 {
		return []Match{}
	}
	tf := map[int]map[string]float64{}
	df := map[string]int{}
	for id, text := range docs {
		m := map[string]float64{}
		for _, w := range Terms(text) {
			m[w]++
		}
		for w := range m {
			df[w]++
		}
		tf[id] = m
	}
	idf := func(w string) float64 { return math.Log(1 + float64(len(docs))/float64(1+df[w])) }
	vec := func(m map[string]float64) (map[string]float64, float64) {
		out, norm := map[string]float64{}, 0.0
		for w, n := range m {
			x := (1 + math.Log(n)) * idf(w)
			out[w] = x
			norm += x * x
		}
		return out, math.Sqrt(norm)
	}
	qc := map[string]float64{}
	for _, w := range q {
		qc[w]++
	}
	qv, qn := vec(qc)

	out := []Match{}
	for id, m := range tf {
		dv, dn := vec(m)
		if dn == 0 || qn == 0 {
			continue
		}
		dot, shared := 0.0, []string{}
		for w, x := range qv {
			if y, ok := dv[w]; ok {
				dot += x * y
				shared = append(shared, w)
			}
		}
		// Una sola palabra en común es casualidad, salvo entre tickets del mismo servicio.
		if len(shared) == 0 || (len(shared) == 1 && bonus[id] == 0) {
			continue
		}
		score := math.Min(1, dot/(qn*dn)+bonus[id])
		if score >= MinMatchScore {
			sort.Strings(shared)
			out = append(out, Match{ID: id, Score: math.Round(score*100) / 100, Shared: shared})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ServiceBonus: cuánto pesa que el ticket y el candidato sean del mismo servicio.
const ServiceBonus = 0.15

// MatchProblems busca, entre los problemas sin resolver, los que se parecen al texto de un incidente.
// Cada problema se describe con su título, descripción, causa raíz, workaround y los títulos de sus incidentes:
// así aprende de cómo la gente describe el síntoma. onlyShared deja solo los errores conocidos con un workaround
// compartido con quienes reportan.
func (t *Tenant) MatchProblems(text, service string, onlyShared bool, limit int) []Match {
	docs, bonus := map[int]string{}, map[int]float64{}
	for _, p := range t.Problems {
		if p.Status == Resuelto || (onlyShared && !(p.Status == ErrorConocido && p.Workaround != "" && p.WorkaroundShared)) {
			continue
		}
		var b strings.Builder
		for _, s := range []string{p.Title, p.Description, p.RootCause, p.Workaround, p.Service} {
			b.WriteString(s + " ")
		}
		for _, inc := range t.Incidents {
			if inc.ProblemID == p.ID {
				b.WriteString(inc.Title + " ")
			}
		}
		docs[p.ID] = b.String()
		if service != "" && p.Service == service {
			bonus[p.ID] = ServiceBonus
		}
	}
	return rank(text, docs, bonus, limit)
}

// SimilarIncidents busca incidentes parecidos a inc que no estén cerrados: candidatos a duplicado
// o a compartir un problema.
func (t *Tenant) SimilarIncidents(inc *Incident, limit int) []Match {
	docs, bonus := map[int]string{}, map[int]float64{}
	for _, o := range t.Incidents {
		if o.ID == inc.ID || o.Closed() {
			continue
		}
		docs[o.ID] = o.Title + " " + o.Description
		if o.Service == inc.Service {
			bonus[o.ID] = ServiceBonus
		}
	}
	return rank(inc.Title+" "+inc.Description, docs, bonus, limit)
}
