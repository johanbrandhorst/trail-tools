package quiz

// seedExplanations are the explanations of the seeded questions, by their
// prompt, which the quiz master reads out when going through the answers.
// Paragraphs are separated by blank lines. They're keyed by prompt so that
// questions seeded before explanations existed get them too.
var seedExplanations = map[string]string{
	// The Shire.
	"What is the name of Frodo's loyal gardener and companion?": `Samwise Gamgee, the gardener at Bag End and son of Hamfast "the Gaffer" Gamgee.

Sam joins the quest by accident: Gandalf catches him eavesdropping under the window while he tells Frodo about the Ring, and sends him along as his "punishment". He ends up carrying Frodo up the slopes of Mount Doom: "I can't carry it for you, but I can carry you!"

Merry and Pippin are Frodo's cousins, and Fredegar "Fatty" Bolger stays behind at Crickhollow to keep up the pretense that Frodo still lives there.`,

	"Who forged the One Ring?": `Sauron, in the fires of Mount Doom, around the year 1600 of the Second Age.

He poured much of his own power into it, so that it could rule the other Rings of Power, which is why destroying it destroys him too.

Celebrimbor is the trap: he and the Elven-smiths of Eregion forged the other rings with Sauron's teaching, and the Three Elven rings by himself, but not the One. Saruman only studied ring-lore, and coveted it.`,

	"What does Gollum call the Ring?": `"My precious", which he says to himself, to the Ring, and later of the Ring when he speaks to it as if it were a person.

"My birthday present" is a deliberate trap: in The Hobbit, Gollum tells himself the Ring was his birthday present, because he strangled his friend Déagol for it on his birthday.`,

	"Where must the Ring be destroyed?": `Mount Doom, also called Orodruin, in Mordor.

The Ring can only be unmade in the fire where it was forged, in the Sammath Naur, the Chambers of Fire inside the mountain. In the end, Frodo can't bring himself to throw it in: it's Gollum, biting off Frodo's finger and then stumbling into the fire, who destroys it.`,

	"What is the name of Bilbo's home in Hobbiton?": `Bag End, a luxurious hobbit-hole at the top of the Hill in Hobbiton, built by Bilbo's father, Bungo Baggins, for his bride, Belladonna Took.

Bilbo leaves it to Frodo, who sells it to the Sackville-Bagginses before he sets out. Brandy Hall is the home of the Brandybucks in Buckland, Crickhollow the house Frodo pretends to move to, and Bywater a village nearby.`,

	"Which member of the Fellowship is an Elf?": `Legolas, son of Thranduil, the Elvenking of northern Mirkwood (the Elvenking of The Hobbit).

He and Gimli start off with the old distrust between Elves and Dwarves, and end the best of friends: at Helm's Deep they count their kills, and Gimli wins by one, forty-two to Legolas's forty-one.`,

	"How many members does the Fellowship of the Ring have?": `Nine: the four hobbits Frodo, Sam, Merry and Pippin, Gandalf, Aragorn, Boromir, Legolas and Gimli.

Elrond chose the number on purpose: nine walkers to set against the nine riders, the Nazgûl.`,

	`"You shall not pass!" Who does Gandalf say it to on the Bridge of Khazad-dûm?`: `The Balrog, Durin's Bane: a demon of shadow and flame, an ancient spirit of the same order as Gandalf himself, awoken when the Dwarves delved too deep for mithril.

A fun detail: in the book, Gandalf says "You cannot pass!" The film made it "You shall not pass!"`,

	"What color does Gandalf become after his return?": `White. Gandalf the Grey falls fighting the Balrog, and is sent back as Gandalf the White, taking the place of Saruman, who had betrayed his order.

At first, Aragorn, Legolas and Gimli mistake him for Saruman in Fangorn Forest, as both are robed in white.`,

	"What is the dwarf of the Fellowship called?": `Gimli, son of Glóin.

Glóin is one of the thirteen dwarves of Thorin's company in The Hobbit, and he comes to the Council of Elrond with his son. Thorin and Balin are also from The Hobbit: the company found Balin's tomb in Moria.`,

	// Rivendell.
	"What is the name of Aragorn's sword, reforged from the shards of Narsil?": `Andúril, "Flame of the West", reforged by the Elven-smiths of Rivendell.

Narsil was the sword of Elendil, which broke when he fell fighting Sauron. Isildur used its shards to cut the Ring from Sauron's hand. In the book, the sword is reforged before the Fellowship sets out; in the films, Elrond brings it to Aragorn much later.

Glamdring is Gandalf's sword, Orcrist Thorin's, and Herugrim Théoden's.`,

	"What is the name of Gandalf's horse, lord of all horses?": `Shadowfax, chief of the Mearas, the horses of the kings of Rohan, who would bear no one else.

Gandalf "borrows" him from Théoden's stables, and Shadowfax comes when he calls. Snowmane is Théoden's horse, Hasufel the horse Éomer lends to Aragorn, and Brego the horse Aragorn rides in the films.`,

	"Which word opens the Doors of Durin, the west gate of Moria?": `Mellon, the Elvish word for "friend".

The arch reads "Ennyn Durin Aran Moria: pedo mellon a minno", "The Doors of Durin, Lord of Moria. Speak, friend, and enter." Gandalf takes it as a riddle and tries spell after spell, until a hobbit's question makes him realize it's a plain invitation from a friendlier age. It's also the sign in for this quiz!`,

	"Who slays the Witch-king of Angmar on the Pelennor Fields?": `Éowyn, niece of Théoden, disguised as a rider of Rohan named Dernhelm.

It had long been foretold that the Witch-king would not fall by the hand of man. "I am no man!" Merry helps: he stabs the Witch-king from behind with his blade from the Barrow-downs, made long ago to fight Angmar, which breaks the spell on him and lets Éowyn strike.`,

	"What is the name of Saruman's tower at Isengard?": `Orthanc, a tower of unbreakable black stone, built by the men of Gondor in their days of glory, in the middle of the ring-wall of Isengard.

It held one of the palantíri, the seeing-stones, through which Saruman was ensnared by Sauron. The Ents flood Isengard, but even they can't harm the tower. Barad-dûr is Sauron's tower, Minas Morgul the Witch-king's city, and Cirith Ungol the pass into Mordor.`,

	"Who is the father of Boromir and Faramir?": `Denethor II, the Steward of Gondor, who ruled from Minas Tirith while there was no king.

Proud and wise, he looked into the palantír of Minas Tirith to fight Sauron, and was shown only what drove him to despair. Ecthelion was Denethor's father, Théoden the king of Rohan, and Imrahil the prince of Dol Amroth.`,

	"What is the name of the inn at Bree where the hobbits meet Strider?": `The Prancing Pony, kept by the forgetful Barliman Butterbur.

There, Frodo accidentally puts on the Ring while singing on a table, vanishing in front of the whole common room. The Green Dragon is the inn at Bywater in the Shire, the Golden Perch in Stock has the best beer in the Eastfarthing, and the Ivy Bush is the Gaffer's local.`,

	"What is the name of the great spider in the pass of Cirith Ungol?": `Shelob, who guards the secret way into Mordor, and whom Gollum leads Frodo and Sam to on purpose.

She is the last child of Ungoliant, the primordial spider who devoured the light of the Two Trees of Valinor with Morgoth, long before. Sam drives her off with Sting and the Phial of Galadriel.`,

	"What is the name of the sword Bilbo gives to Frodo?": `Sting, which glows blue when orcs are near.

It's an Elvish knife, a sword to a hobbit, that Bilbo found in the trolls' lair in The Hobbit, and named after he fought the great spiders of Mirkwood with it. Narsil is Elendil's sword, Ringil was Fingolfin's, and Anglachel was Túrin's.`,

	"In which age of Middle-earth is the War of the Ring fought?": `The Third Age, in its last years, 3018 and 3019.

The destruction of the One Ring ends it: the Three Elven rings lose their power, the Elves sail west, and the Fourth Age, the age of Men, begins.`,

	// Mordor.
	"Who found the One Ring in the Gladden Fields, before Sméagol took it from him?": `Déagol, Sméagol's friend and kinsman.

The Ring had lain on the bed of the Anduin for some two and a half thousand years, since Isildur was killed there and lost it. While fishing on Sméagol's birthday, Déagol was pulled into the river and came up with it. Sméagol strangled him for it, and was cast out by his people, becoming Gollum.`,

	"How many Rings of Power were given to the Dwarf-lords?": `Seven: "Three Rings for the Elven-kings under the sky, Seven for the Dwarf-lords in their halls of stone, Nine for Mortal Men doomed to die."

The Dwarves proved hard to dominate: the rings only kindled their greed for gold. Sauron recovered three of the Seven, and dragons consumed the other four.`,

	"Under what name did Aragorn serve Rohan and Gondor in his youth?": `Thorongil, "Eagle of the Star", under which he served King Thengel of Rohan and Ecthelion, the Steward of Gondor, and led a daring raid on the Corsairs of Umbar.

Denethor, Ecthelion's son, always suspected who he was, and resented him. Estel ("Hope") is Aragorn's childhood name in Rivendell, Elessar ("Elfstone") the name he takes as king, and Telcontar ("Strider" in Quenya) the name of his royal house.`,

	"Who is Tom Bombadil's wife, the River-daughter?": `Goldberry, daughter of the River-woman of the Withywindle.

She and Tom shelter the hobbits in the Old Forest. Both are left out of the films, which makes this one of the hard questions. Nimrodel is an Elf-maiden of Lórien, and Lúthien the Elf who loved Beren, whose tale Aragorn sings on Weathertop.`,

	"Which of these is not one of the Istari, the wizards sent to Middle-earth?": `Glorfindel, an Elf-lord of Rivendell, who came back from the dead to Middle-earth long ago.

In the book, it's he who rides out to find Frodo and puts him on his horse to reach the Ford of Bruinen, a role the films gave to Arwen. The five Istari are Gandalf, Saruman, Radagast and the two Blue Wizards, Alatar and Pallando, who went into the East.`,

	"Which of the three Elven rings does Galadriel bear?": `Nenya, the Ring of Water, made of mithril with a white stone, which preserves Lothlórien from the decay of time. Frodo sees it on her finger at the Mirror.

Narya, the Ring of Fire, is borne by Gandalf, given to him by Círdan. Vilya, the Ring of Air and mightiest of the three, is borne by Elrond. The Nauglamír is the Necklace of the Dwarves, from The Silmarillion.`,

	"What is the name of the hasty young Ent who looks after Merry and Pippin?": `Quickbeam, or Bregalad in Elvish, so called because he once answered an elder Ent "yes" before the question was finished.

He looks after the hobbits during the Entmoot. Orcs from Isengard had cut down his beloved rowan trees, so he's all for marching on Saruman. Skinbark and Leaflock are other old Ents, and Fimbrethil is the Entwife Treebeard lost.`,

	"How old does Bilbo turn at his farewell party?": `Eleventy-one, or one hundred and eleven, on the same day Frodo turned thirty-three, the age hobbits come of age.

Together they make one hundred and forty-four, one gross, which is why there were exactly that many guests. Bilbo ends his speech by vanishing with the Ring.`,

	"What is the name of the pony the Fellowship sends away at the gates of Moria?": `Bill, the half-starved pony Sam buys from Bill Ferny in Bree and nurses back to health.

Sam is heartbroken to let him go at the gates of Moria, but Bill finds his own way back to Bree, where Sam finds him again on the way home. Fatty Lumpkin is Tom Bombadil's pony, and Sharkey the name Saruman goes by in the Shire.`,

	"In the books, what is Galadriel's gift to Sam?": `A little box of earth from her orchard, with a mallorn seed in it.

After the Scouring of the Shire, Sam scatters the earth to heal the land, and plants the seed in the Party Field, where the only mallorn tree west of the mountains grows. In the films, Sam gets Elven rope instead. The phial goes to Frodo and the green elfstone to Aragorn.`,
}
