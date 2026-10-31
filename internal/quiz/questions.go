package quiz

// Tier is the difficulty of a quiz.
type Tier string

// The tiers, from easiest to hardest. Each is a quiz of its own, named after
// a place in Middle-earth.
const (
	TierEasy   Tier = "easy"
	TierMedium Tier = "medium"
	TierHard   Tier = "hard"
)

// tiers lists the tiers in order.
var tiers = []Tier{TierEasy, TierMedium, TierHard}

// points is how many points a correct answer is worth in each tier, so the
// hard questions count for more on the leaderboard.
var points = map[Tier]int{TierEasy: 1, TierMedium: 2, TierHard: 3}

// validTier returns whether t is one of the tiers.
func validTier(t Tier) bool {
	_, ok := points[t]
	return ok
}

// seedQuestions are the questions the quiz starts with. The quiz master can
// edit them on the quiz's admin page.
var seedQuestions = func() []Question {
	// The correct answer comes first here, and is moved to a position
	// from answerSpots below, so the answers don't follow a pattern.
	q := func(tier Tier, prompt, answer string, wrong ...string) Question {
		return Question{Tier: tier, Prompt: prompt, Choices: append([]string{answer}, wrong...)}
	}
	all := []Question{
		// The Shire: what every hobbit knows.
		q(TierEasy, "What is the name of Frodo's loyal gardener and companion?",
			"Samwise Gamgee", "Meriadoc Brandybuck", "Peregrin Took", "Fredegar Bolger"),
		q(TierEasy, "Who forged the One Ring?",
			"Sauron", "Saruman", "Celebrimbor", "Gandalf"),
		q(TierEasy, "What does Gollum call the Ring?",
			"My precious", "My treasure", "My birthday present", "My darling"),
		q(TierEasy, "Where must the Ring be destroyed?",
			"Mount Doom", "Weathertop", "Isengard", "The Mines of Moria"),
		q(TierEasy, "What is the name of Bilbo's home in Hobbiton?",
			"Bag End", "Brandy Hall", "Crickhollow", "Bywater"),
		q(TierEasy, "Which member of the Fellowship is an Elf?",
			"Legolas", "Gimli", "Boromir", "Aragorn"),
		q(TierEasy, "How many members does the Fellowship of the Ring have?",
			"Nine", "Seven", "Eight", "Twelve"),
		q(TierEasy, `"You shall not pass!" Who does Gandalf say it to on the Bridge of Khazad-dûm?`,
			"The Balrog", "Saruman", "A cave troll", "The Witch-king"),
		q(TierEasy, "What color does Gandalf become after his return?",
			"White", "Grey", "Blue", "Brown"),
		q(TierEasy, "What is the dwarf of the Fellowship called?",
			"Gimli", "Thorin", "Balin", "Glóin"),

		// Rivendell: for those who have studied in Elrond's library.
		q(TierMedium, "What is the name of Aragorn's sword, reforged from the shards of Narsil?",
			"Andúril", "Glamdring", "Orcrist", "Herugrim"),
		q(TierMedium, "What is the name of Gandalf's horse, lord of all horses?",
			"Shadowfax", "Brego", "Hasufel", "Snowmane"),
		q(TierMedium, "Which word opens the Doors of Durin, the west gate of Moria?",
			"Mellon", "Edro", "Mithril", "Elendil"),
		q(TierMedium, "Who slays the Witch-king of Angmar on the Pelennor Fields?",
			"Éowyn", "Aragorn", "Gandalf", "Éomer"),
		q(TierMedium, "What is the name of Saruman's tower at Isengard?",
			"Orthanc", "Barad-dûr", "Minas Morgul", "Cirith Ungol"),
		q(TierMedium, "Who is the father of Boromir and Faramir?",
			"Denethor", "Théoden", "Imrahil", "Ecthelion"),
		q(TierMedium, "What is the name of the inn at Bree where the hobbits meet Strider?",
			"The Prancing Pony", "The Green Dragon", "The Golden Perch", "The Ivy Bush"),
		q(TierMedium, "What is the name of the great spider in the pass of Cirith Ungol?",
			"Shelob", "Ungoliant", "Mirkwood", "Gothmog"),
		q(TierMedium, "What is the name of the sword Bilbo gives to Frodo?",
			"Sting", "Narsil", "Ringil", "Anglachel"),
		q(TierMedium, "In which age of Middle-earth is the War of the Ring fought?",
			"The Third Age", "The Second Age", "The Fourth Age", "The First Age"),

		// Mordor: one does not simply know these.
		q(TierHard, "Who found the One Ring in the Gladden Fields, before Sméagol took it from him?",
			"Déagol", "Isildur", "Bandobras", "Fréaláf"),
		q(TierHard, "How many Rings of Power were given to the Dwarf-lords?",
			"Seven", "Three", "Nine", "Nineteen"),
		q(TierHard, "Under what name did Aragorn serve Rohan and Gondor in his youth?",
			"Thorongil", "Elessar", "Estel", "Telcontar"),
		q(TierHard, "Who is Tom Bombadil's wife, the River-daughter?",
			"Goldberry", "Arwen", "Lúthien", "Nimrodel"),
		q(TierHard, "Which of these is not one of the Istari, the wizards sent to Middle-earth?",
			"Glorfindel", "Radagast", "Alatar", "Pallando"),
		q(TierHard, "Which of the three Elven rings does Galadriel bear?",
			"Nenya", "Narya", "Vilya", "Nauglamír"),
		q(TierHard, "What is the name of the hasty young Ent who looks after Merry and Pippin?",
			"Quickbeam", "Skinbark", "Leaflock", "Fimbrethil"),
		q(TierHard, "How old does Bilbo turn at his farewell party?",
			"Eleventy-one", "Ninety-nine", "One hundred", "One hundred and one"),
		q(TierHard, "What is the name of the pony the Fellowship sends away at the gates of Moria?",
			"Bill", "Fatty Lumpkin", "Strider", "Sharkey"),
		q(TierHard, "In the books, what is Galadriel's gift to Sam?",
			"A box of earth from her orchard", "A coil of Elven rope", "A phial of light", "A green elfstone"),
	}
	answerSpots := []int{2, 0, 3, 1, 1, 3, 0, 2, 3, 0, 1, 2, 0}
	position := map[Tier]int{}
	for i := range all {
		q := &all[i]
		q.Position = position[q.Tier]
		position[q.Tier]++
		spot := answerSpots[(i+q.Position)%len(answerSpots)] % len(q.Choices)
		q.Choices[0], q.Choices[spot] = q.Choices[spot], q.Choices[0]
		q.AnswerIndex = spot
	}
	return all
}()
