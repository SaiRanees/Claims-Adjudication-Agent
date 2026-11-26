package main

import "github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"

// The seeded scenarios' clinical content and prices. Every hospital,
// vendor, person and number is invented; any likeness to a real one is
// chance. Descriptions avoid the items Annexure A lists, except where a
// line is deliberately listed through datagen's Annexure table.

// condition is one scenario's illness or injury, as the discharge summary
// and OT notes describe it.
type condition struct {
	Diagnosis   string
	Procedure   string
	Listed      string // the Excl02 group, if the condition is listed
	PreExisting bool
	Tests       []string
	History     string
	Course      string
	Advice      string
	// Nights is the stay's range of nights.
	Nights [2]int
	// Surgery, if any, is the operation and what it implants.
	Surgery *operation
}

// operation is a surgery: its fees and, if it implants one, the implant.
type operation struct {
	Anaesthesia string
	Findings    string
	SurgeonFee  int64 // rupees
	Theatre     string
	TheatreFee  int64 // rupees
	// Implant is empty for an operation without one.
	Implant      string
	ImplantPrice [2]int64 // rupees, a range
}

var medicalConditions = []condition{
	{Diagnosis: "Dengue fever with thrombocytopenia", Nights: [2]int{3, 5},
		Tests:   []string{"Complete Blood Count", "Dengue NS1 Antigen", "Liver Function Test", "Ultrasound Abdomen"},
		History: "Fever with body ache and headache for four days, with a rash on the arms since yesterday. Platelets 62,000 on an outside test.",
		Course:  "Treated with intravenous fluids and close monitoring of platelets, which reached their lowest on the third day and recovered after. No bleeding.",
		Advice:  "Plenty of oral fluids. Tab. Paracetamol 650 mg when needed for fever. Avoid painkillers other than paracetamol. Repeat the blood count after three days."},
	{Diagnosis: "Community-acquired pneumonia", Nights: [2]int{4, 6},
		Tests:   []string{"Complete Blood Count", "X-Ray Chest PA View", "Blood Culture and Sensitivity", "Renal Function Test"},
		History: "Cough with yellow sputum and high fever for five days, breathless on walking for two days. Crackles at the right lung base.",
		Course:  "Started on intravenous antibiotics and oxygen by nasal prongs, which was stopped on the third day. Fever settled within 48 hours.",
		Advice:  "Complete the oral antibiotic course. Breathing exercises. Review in the medicine clinic after one week with a repeat chest X-ray."},
	{Diagnosis: "Acute gastroenteritis with severe dehydration", Nights: [2]int{3, 4},
		Tests:   []string{"Complete Blood Count", "Serum Electrolytes", "Renal Function Test", "Urine Routine Examination"},
		History: "Loose stools more than ten times a day with vomiting for two days, unable to keep fluids down. Dizzy on standing.",
		Course:  "Rehydrated with intravenous fluids and given antiemetics. Electrolytes corrected by the second day, and oral intake resumed.",
		Advice:  "Oral rehydration solution after each loose stool. Light diet for three days. Return if the stools become bloody or the vomiting returns."},
	{Diagnosis: "Acute pyelonephritis", Nights: [2]int{3, 5},
		Tests:   []string{"Complete Blood Count", "Urine Routine Examination", "Ultrasound Abdomen", "Renal Function Test"},
		History: "Fever with chills, pain in the left flank and burning on passing urine for three days.",
		Course:  "Treated with intravenous antibiotics, changed to oral after the urine culture report. Flank pain and fever resolved.",
		Advice:  "Complete the antibiotic course. Drink at least three litres of water a day. Repeat the urine examination after two weeks."},
	{Diagnosis: "Uncontrolled type 2 diabetes with ketosis", PreExisting: true, Nights: [2]int{3, 5},
		Tests:   []string{"HbA1c", "Serum Electrolytes", "Renal Function Test", "Urine Routine Examination"},
		History: "Known diabetic for nine years on oral medicines. Thirst, frequent urination and tiredness for a week; sugars above 400 at home.",
		Course:  "Sugars controlled with an insulin infusion and then subcutaneous insulin. Ketones cleared on the second day. Diabetes education given.",
		Advice:  "Insulin as charted twice daily with home sugar monitoring. Diabetic diet. Review in the diabetes clinic after one week with the sugar chart."},
	{Diagnosis: "Acute exacerbation of bronchial asthma", PreExisting: true, Nights: [2]int{3, 4},
		Tests:   []string{"Complete Blood Count", "X-Ray Chest PA View", "Arterial Blood Gas"},
		History: "Asthmatic since childhood. Wheeze and breathlessness for two days after a cold, not relieved by the inhaler at home.",
		Course:  "Treated with nebulised bronchodilators, intravenous steroids and oxygen. Wheeze settled and the steroid was changed to oral.",
		Advice:  "Inhalers as shown, with the spacer. Tapering course of oral steroid. Avoid dust and smoke. Review in the chest clinic after two weeks."},
	{Diagnosis: "Typhoid fever", Nights: [2]int{4, 6},
		Tests:   []string{"Complete Blood Count", "Widal Test", "Blood Culture and Sensitivity", "Liver Function Test"},
		History: "Step-ladder fever for eight days with loss of appetite and abdominal discomfort. Took oral antibiotics from a clinic without relief.",
		Course:  "Blood culture grew Salmonella typhi. Treated with intravenous antibiotics; fever settled on the fifth day.",
		Advice:  "Complete the oral antibiotic course. Boiled water and home-cooked food. Review after two weeks."},
	{Diagnosis: "Hypertensive urgency", PreExisting: true, Nights: [2]int{3, 3},
		Tests:   []string{"ECG", "Renal Function Test", "Serum Electrolytes", "2D Echocardiography"},
		History: "Known hypertensive for six years, irregular with medicines. Severe headache with blood pressure of 210/120 at a clinic.",
		Course:  "Blood pressure lowered gradually with oral medicines over 48 hours. No damage to the heart, kidneys or eyes was found.",
		Advice:  "Take the blood pressure medicines daily without a break. Low-salt diet. Check the blood pressure at home and review after one week."},
}

var surgeryConditions = []condition{
	{Diagnosis: "Primary osteoarthritis of the right knee", Procedure: "Total knee replacement, right", Listed: "listed-36-months", Nights: [2]int{4, 5},
		Tests:   []string{"X-Ray Right Knee AP and Lateral", "Complete Blood Count", "Coagulation Profile", "ECG"},
		History: "Pain in the right knee for six years, worse over the last year, with difficulty climbing stairs. No relief from medicines and physiotherapy.",
		Course:  "Underwent total knee replacement under spinal anaesthesia. Mobilised with a walker on the first day after surgery. The wound is healthy.",
		Advice:  "Walk with the walker and do the knee exercises as taught. Stitches to be removed after twelve days. Review in the orthopaedic clinic after two weeks.",
		Surgery: &operation{Anaesthesia: "Spinal", Findings: "Advanced arthritis of all three compartments with a varus deformity. A cemented prosthesis was fixed.",
			SurgeonFee: 60000, Theatre: "Operation Theatre Charges", TheatreFee: 25000, Implant: "Knee Prosthesis - Cemented", ImplantPrice: [2]int64{85000, 110000}}},
	{Diagnosis: "Right inguinal hernia", Procedure: "Open mesh hernioplasty", Listed: "listed-24-months", Nights: [2]int{2, 3},
		Tests:   []string{"Complete Blood Count", "Ultrasound Abdomen", "ECG"},
		History: "Swelling in the right groin for eight months, increasing on standing and coughing, with a dragging pain.",
		Course:  "Underwent open mesh hernioplasty under spinal anaesthesia. Recovery was uneventful and the patient was mobilised the next morning.",
		Advice:  "Avoid lifting heavy weights for six weeks. Keep the wound dry. Review in the surgical clinic after one week.",
		Surgery: &operation{Anaesthesia: "Spinal", Findings: "An indirect inguinal hernia with a 3 cm defect. The sac was reduced and a polypropylene mesh fixed.",
			SurgeonFee: 30000, Theatre: "Operation Theatre Charges", TheatreFee: 15000, Implant: "Polypropylene Mesh 15 x 15 cm", ImplantPrice: [2]int64{8000, 14000}}},
	{Diagnosis: "Coronary artery disease, single vessel", Procedure: "Coronary angioplasty with stenting", Nights: [2]int{2, 3},
		Tests:   []string{"ECG", "Troponin I", "2D Echocardiography", "Coronary Angiography", "Renal Function Test"},
		History: "Chest pain on exertion for three months, relieved by rest. A treadmill test showed changes of reduced blood flow.",
		Course:  "Angiography showed a 90% block in the left anterior descending artery, opened with a stent. No complications. Chest pain free since.",
		Advice:  "Tab. Aspirin and Tab. Clopidogrel daily without a break for one year, and the other medicines as listed. Cardiac rehabilitation. Review after two weeks.",
		Surgery: &operation{Anaesthesia: "Local", Findings: "A 90% block in the proximal left anterior descending artery. One drug eluting stent placed, with good flow after.",
			SurgeonFee: 40000, Theatre: "Cath Lab Charges", TheatreFee: 30000, Implant: "Drug Eluting Coronary Stent", ImplantPrice: [2]int64{28000, 34000}}},
	{Diagnosis: "Complete heart block", Procedure: "Permanent pacemaker implantation", Nights: [2]int{3, 4},
		Tests:   []string{"ECG", "2D Echocardiography", "Serum Electrolytes", "Thyroid Profile"},
		History: "Two fainting spells in a week, with a slow pulse of 34 a minute at a clinic. No chest pain.",
		Course:  "A temporary pacing wire was placed, then a dual chamber pacemaker implanted under local anaesthesia. Pacing checks were normal.",
		Advice:  "Do not raise the left arm above the shoulder for four weeks. Carry the pacemaker card. Pacemaker check after six weeks.",
		Surgery: &operation{Anaesthesia: "Local", Findings: "Complete heart block with a ventricular rate of 34. A dual chamber pacemaker was implanted through the left subclavian vein.",
			SurgeonFee: 45000, Theatre: "Cath Lab Charges", TheatreFee: 30000, Implant: "Dual Chamber Pacemaker", ImplantPrice: [2]int64{95000, 130000}}},
	{Diagnosis: "Avascular necrosis of the left hip", Procedure: "Total hip replacement, left", Listed: "listed-36-months", Nights: [2]int{4, 6},
		Tests:   []string{"X-Ray of the Injured Part", "Complete Blood Count", "Coagulation Profile", "ECG"},
		History: "Pain in the left hip for two years, now limping and unable to sit cross-legged. MRI showed collapse of the femoral head.",
		Course:  "Underwent total hip replacement under spinal anaesthesia. Mobilised with a walker on the second day. The wound is healthy.",
		Advice:  "Do not cross the legs or sit on low chairs for six weeks. Walker for four weeks. Review in the orthopaedic clinic after two weeks.",
		Surgery: &operation{Anaesthesia: "Spinal", Findings: "A collapsed femoral head with early arthritis of the socket. An uncemented prosthesis was fixed.",
			SurgeonFee: 65000, Theatre: "Operation Theatre Charges", TheatreFee: 28000, Implant: "Hip Prosthesis - Uncemented", ImplantPrice: [2]int64{90000, 120000}}},
	{Diagnosis: "Left ureteric calculus with hydronephrosis", Procedure: "Ureteroscopic lithotripsy with DJ stenting", Listed: "listed-24-months", Nights: [2]int{2, 3},
		Tests:   []string{"CT Abdomen with Contrast", "Urine Routine Examination", "Renal Function Test", "Complete Blood Count"},
		History: "Colicky pain in the left loin radiating to the groin for a week, with vomiting. CT showed an 8 mm stone in the lower ureter.",
		Course:  "Underwent ureteroscopic lithotripsy under spinal anaesthesia; the stone was broken and removed and a DJ stent placed. Pain free after.",
		Advice:  "Drink three litres of water a day. The DJ stent is to be removed after four weeks; do not miss this. Review in the urology clinic.",
		Surgery: &operation{Anaesthesia: "Spinal", Findings: "An 8 mm stone impacted in the left lower ureter, broken with a laser and cleared. A 6 Fr DJ stent placed.",
			SurgeonFee: 28000, Theatre: "Operation Theatre Charges", TheatreFee: 18000, Implant: "Double J Ureteric Stent", ImplantPrice: [2]int64{2500, 4500}}},
	{Diagnosis: "Primary osteoarthritis of the left knee", Procedure: "Total knee replacement, left", Listed: "listed-36-months", Nights: [2]int{4, 5},
		Tests:   []string{"X-Ray of the Injured Part", "Complete Blood Count", "Coagulation Profile", "ECG"},
		History: "Pain and stiffness in the left knee for five years, now unable to walk more than a hundred metres.",
		Course:  "Underwent total knee replacement under spinal anaesthesia. Knee bending reached 90 degrees before discharge. The wound is healthy.",
		Advice:  "Knee exercises as taught, three times a day. Stitches to be removed after twelve days. Review in the orthopaedic clinic after two weeks.",
		Surgery: &operation{Anaesthesia: "Spinal", Findings: "Severe arthritis of the inner and kneecap compartments. A cemented prosthesis was fixed.",
			SurgeonFee: 60000, Theatre: "Operation Theatre Charges", TheatreFee: 25000, Implant: "Knee Prosthesis - Cemented", ImplantPrice: [2]int64{85000, 110000}}},
	{Diagnosis: "Umbilical hernia", Procedure: "Open mesh repair of umbilical hernia", Listed: "listed-24-months", Nights: [2]int{2, 2},
		Tests:   []string{"Complete Blood Count", "Ultrasound Abdomen", "ECG"},
		History: "Swelling at the navel for a year, increasing in size, with discomfort after meals.",
		Course:  "Underwent open mesh repair under general anaesthesia. Recovery was uneventful and a normal diet was taken the next day.",
		Advice:  "Abdominal binder for four weeks. Avoid lifting heavy weights for six weeks. Review in the surgical clinic after one week.",
		Surgery: &operation{Anaesthesia: "General", Findings: "A 2.5 cm defect at the navel containing omentum. The contents were reduced and a mesh placed.",
			SurgeonFee: 28000, Theatre: "Operation Theatre Charges", TheatreFee: 15000, Implant: "Polypropylene Mesh 7.5 x 15 cm", ImplantPrice: [2]int64{6000, 9000}}},
}

var cataractConditions = []condition{
	{Diagnosis: "Immature senile cataract", Procedure: "Phacoemulsification with foldable IOL"},
	{Diagnosis: "Mature senile cataract", Procedure: "Phacoemulsification with foldable IOL"},
	{Diagnosis: "Nuclear sclerotic cataract", Procedure: "Phacoemulsification with foldable IOL"},
	{Diagnosis: "Posterior subcapsular cataract", Procedure: "Phacoemulsification with foldable IOL"},
}

// cataractCare is every cataract packet's course and advice.
var cataractCare = condition{
	History: "Gradually blurring vision in the eye for a year, with glare at night. Unable to read or recognise faces at a distance.",
	Course:  "Underwent phacoemulsification with a foldable lens implant under topical anaesthesia as a day care procedure. The eye was quiet and vision improved on the first check.",
	Advice:  "Eye drops as charted for four weeks. Wear the protective glasses outdoors. Do not rub the eye or wash the head for a week. Review the next day and after one week.",
}

var icuConditions = []condition{
	{Diagnosis: "Acute anterior wall myocardial infarction",
		Tests:   []string{"ECG", "Troponin I", "2D Echocardiography", "Renal Function Test", "Serum Electrolytes"},
		History: "Sudden crushing chest pain with sweating for two hours before arrival. ECG showed an anterior wall infarction.",
		Course:  "Treated in the ICU with blood thinners and monitoring; thrombolysed on arrival with good response. Moved to the ward once stable and pain free.",
		Advice:  "Medicines as listed, daily without a break. No driving or exertion for four weeks. Review in the cardiology clinic after one week for angiography."},
	{Diagnosis: "Severe sepsis from a urinary tract infection",
		Tests:   []string{"Complete Blood Count", "Blood Culture and Sensitivity", "Urine Routine Examination", "Renal Function Test", "Arterial Blood Gas"},
		History: "Fever with confusion and low urine output for a day. Blood pressure 80/50 on arrival.",
		Course:  "Resuscitated with fluids and given intravenous antibiotics in the ICU; needed blood pressure support for 36 hours. Moved to the ward once stable.",
		Advice:  "Complete the antibiotic course. Plenty of fluids. Repeat kidney function tests after one week."},
	{Diagnosis: "Diabetic ketoacidosis", PreExisting: true,
		Tests:   []string{"Arterial Blood Gas", "Serum Electrolytes", "Renal Function Test", "HbA1c", "Urine Routine Examination"},
		History: "Known diabetic for twelve years. Vomiting, abdominal pain and fast breathing for a day after missing insulin.",
		Course:  "Treated in the ICU with an insulin infusion, fluids and potassium. Acidosis cleared in 24 hours. Moved to the ward on subcutaneous insulin.",
		Advice:  "Never stop insulin, even when unwell. Home sugar monitoring four times a day. Review in the diabetes clinic after one week."},
	{Diagnosis: "Acute ischaemic stroke",
		Tests:   []string{"CT Brain Plain", "MRI Brain", "ECG", "2D Echocardiography", "Coagulation Profile"},
		History: "Sudden weakness of the right arm and leg with slurred speech, noticed on waking. No loss of consciousness.",
		Course:  "Monitored in the ICU. MRI confirmed an infarct in the left hemisphere. Started on blood thinners and physiotherapy; speech and power improving.",
		Advice:  "Medicines as listed. Daily physiotherapy and speech therapy. Review in the neurology clinic after two weeks."},
	{Diagnosis: "Severe acute pancreatitis",
		Tests:   []string{"Serum Amylase and Lipase", "CT Abdomen with Contrast", "Complete Blood Count", "Liver Function Test", "Serum Electrolytes"},
		History: "Severe upper abdominal pain going to the back, with vomiting, for a day. Lipase five times normal.",
		Course:  "Managed in the ICU with fluids, pain relief and bowel rest. Pain settled and feeds were started by the fourth day. Moved to the ward.",
		Advice:  "Low-fat diet. No alcohol. Review in the gastroenterology clinic after two weeks with a repeat ultrasound."},
	{Diagnosis: "Acute exacerbation of COPD with respiratory failure", PreExisting: true,
		Tests:   []string{"Arterial Blood Gas", "X-Ray Chest PA View", "Complete Blood Count", "ECG"},
		History: "Known COPD for eight years. Breathlessness at rest and drowsiness for a day; carbon dioxide high on the blood gas.",
		Course:  "Treated in the ICU with non-invasive ventilation, nebulisers, steroids and antibiotics. Weaned off the support and moved to the ward.",
		Advice:  "Inhalers as shown. Stop smoking. Pulmonary rehabilitation. Review in the chest clinic after two weeks."},
	{Diagnosis: "Dengue shock syndrome",
		Tests:   []string{"Complete Blood Count", "Dengue NS1 Antigen", "Liver Function Test", "Coagulation Profile", "Ultrasound Abdomen"},
		History: "Fever for five days, then cold hands, abdominal pain and low blood pressure as the fever came down.",
		Course:  "Resuscitated with fluids in the ICU with close monitoring of blood counts. Shock resolved in 24 hours and platelets recovered. Moved to the ward.",
		Advice:  "Plenty of oral fluids. Tab. Paracetamol only, when needed. Repeat the blood count after three days."},
	{Diagnosis: "Status epilepticus", PreExisting: true,
		Tests:   []string{"CT Brain Plain", "Serum Electrolytes", "Renal Function Test", "Complete Blood Count"},
		History: "Known epileptic for ten years. Repeated fits without regaining consciousness for thirty minutes after missing medicines.",
		Course:  "Fits controlled with intravenous medicines in the ICU. Regained full consciousness on the second day. Moved to the ward on oral medicines.",
		Advice:  "Never miss the fit medicines. No driving, swimming or working at heights. Review in the neurology clinic after two weeks."},
}

var accidentConditions = []condition{
	{Diagnosis: "Fracture shaft of the right femur", Procedure: "Intramedullary interlocking nailing, right femur", Nights: [2]int{4, 6},
		Tests:   []string{"X-Ray of the Injured Part", "Complete Blood Count", "Coagulation Profile", "ECG"},
		History: "Fell from a two-wheeler after a collision on the road. Pain and deformity of the right thigh; unable to stand. Brought by ambulance.",
		Course:  "The fracture was fixed with an interlocking nail under spinal anaesthesia. Mobilised with a walker without bearing weight on the leg.",
		Advice:  "No weight on the right leg for six weeks. Walker at all times. Review in the orthopaedic clinic after two weeks with an X-ray.",
		Surgery: &operation{Anaesthesia: "Spinal", Findings: "A transverse fracture of the middle third of the femur, reduced closed and fixed with an interlocking nail.",
			SurgeonFee: 45000, Theatre: "Operation Theatre Charges", TheatreFee: 22000, Implant: "Intramedullary Interlocking Nail", ImplantPrice: [2]int64{22000, 32000}}},
	{Diagnosis: "Fracture of the left distal radius", Procedure: "Closed reduction and plaster cast", Nights: [2]int{2, 2},
		Tests:   []string{"X-Ray of the Injured Part", "Complete Blood Count"},
		History: "Slipped and fell on an outstretched left hand on a wet road. Swelling and deformity of the wrist. Brought by ambulance.",
		Course:  "The fracture was reduced under general anaesthesia and a plaster cast applied. The position on X-ray is good.",
		Advice:  "Keep the arm raised. Move the fingers often. Keep the cast dry. Review in the orthopaedic clinic after one week with an X-ray.",
		Surgery: &operation{Anaesthesia: "General", Findings: "A displaced fracture of the lower end of the radius, reduced closed to a good position and held in a cast.",
			SurgeonFee: 15000, Theatre: "Operation Theatre Charges", TheatreFee: 8000}},
	{Diagnosis: "Head injury with scalp laceration", Nights: [2]int{2, 3},
		Tests:   []string{"CT Brain Plain", "X-Ray of the Injured Part", "Complete Blood Count"},
		History: "Hit by a car while crossing the road. Brief loss of consciousness and a bleeding cut on the scalp. Brought by ambulance.",
		Course:  "CT of the brain showed no bleeding. The scalp wound was cleaned and sutured. Observed for 48 hours with no worsening.",
		Advice:  "Return at once for vomiting, drowsiness or a fit. Sutures to be removed after seven days. Rest for a week."},
	{Diagnosis: "Bimalleolar fracture of the right ankle", Procedure: "Open reduction and internal fixation, right ankle", Nights: [2]int{3, 4},
		Tests:   []string{"X-Ray of the Injured Part", "Complete Blood Count", "Coagulation Profile"},
		History: "Twisted the right ankle falling down a flight of stairs at a railway station. Swelling and inability to stand. Brought by ambulance.",
		Course:  "Both fractures were fixed with a plate and screws under spinal anaesthesia. Mobilised with a walker without bearing weight.",
		Advice:  "No weight on the right foot for six weeks. Keep the leg raised. Review in the orthopaedic clinic after two weeks.",
		Surgery: &operation{Anaesthesia: "Spinal", Findings: "Fractures of both malleoli with a shift of the ankle. Fixed with a locking plate on the outer side and screws on the inner.",
			SurgeonFee: 40000, Theatre: "Operation Theatre Charges", TheatreFee: 20000, Implant: "Locking Plate and Screw Set", ImplantPrice: [2]int64{18000, 26000}}},
	{Diagnosis: "Multiple rib fractures with lung contusion", Nights: [2]int{4, 5},
		Tests:   []string{"CT Chest", "X-Ray Chest PA View", "Arterial Blood Gas", "Complete Blood Count"},
		History: "Thrown from an autorickshaw when it overturned. Pain on breathing over the left chest. Brought by ambulance.",
		Course:  "Treated with pain relief, oxygen and breathing exercises. The lung bruise settled on repeat X-ray and oxygen was stopped.",
		Advice:  "Breathing exercises every hour while awake. Pain medicines as charted. Review in the surgical clinic after one week."},
	{Diagnosis: "Fracture of the left clavicle", Procedure: "Open reduction and plate fixation, left clavicle", Nights: [2]int{2, 3},
		Tests:   []string{"X-Ray of the Injured Part", "Complete Blood Count", "ECG"},
		History: "Fell from a bicycle onto the left shoulder after hitting a pothole. Pain and a bump over the collar bone. Brought by ambulance.",
		Course:  "The fracture was fixed with a plate under general anaesthesia. Shoulder movements started gently. The wound is healthy.",
		Advice:  "Arm sling for three weeks. No lifting with the left arm for six weeks. Review in the orthopaedic clinic after two weeks.",
		Surgery: &operation{Anaesthesia: "General", Findings: "A displaced fracture of the middle third of the clavicle, fixed with a locking plate.",
			SurgeonFee: 35000, Theatre: "Operation Theatre Charges", TheatreFee: 18000, Implant: "Clavicle Locking Plate", ImplantPrice: [2]int64{14000, 19000}}},
	{Diagnosis: "Lacerated wound of the right forearm", Procedure: "Wound debridement and suturing", Nights: [2]int{2, 2},
		Tests:   []string{"X-Ray of the Injured Part", "Complete Blood Count"},
		History: "Cut on the right forearm by broken glass when a bus window shattered in a collision. Brought by ambulance.",
		Course:  "The wound was cleaned and the damaged tissue removed under regional anaesthesia, then sutured. No injury to tendons or nerves.",
		Advice:  "Keep the dressing dry. Antibiotics and tetanus cover given. Sutures to be removed after ten days.",
		Surgery: &operation{Anaesthesia: "Regional block", Findings: "A 9 cm wound through skin and fat with no tendon, nerve or vessel injury. Debrided and closed in layers.",
			SurgeonFee: 12000, Theatre: "Minor OT Charges", TheatreFee: 6000}},
	{Diagnosis: "Fracture of the right patella", Procedure: "Tension band wiring, right patella", Nights: [2]int{3, 4},
		Tests:   []string{"X-Ray of the Injured Part", "Complete Blood Count", "Coagulation Profile"},
		History: "Fell on the right knee getting off a moving bus. Swelling of the knee and unable to straighten the leg. Brought by ambulance.",
		Course:  "The kneecap was fixed with wires under spinal anaesthesia. Knee kept straight in a brace. Mobilised with a walker.",
		Advice:  "Knee brace for four weeks. Walker for three weeks. Review in the orthopaedic clinic after two weeks.",
		Surgery: &operation{Anaesthesia: "Spinal", Findings: "A transverse fracture of the kneecap with a 6 mm gap, reduced and fixed with two wires and a tension band.",
			SurgeonFee: 30000, Theatre: "Operation Theatre Charges", TheatreFee: 16000, Implant: "Tension Band Wire Set", ImplantPrice: [2]int64{5000, 8000}}},
}

var longConditions = []condition{
	{Diagnosis: "Tuberculous meningitis",
		Tests:   []string{"CSF Analysis", "MRI Brain", "Complete Blood Count", "Liver Function Test", "Serum Electrolytes", "X-Ray Chest PA View"},
		History: "Headache and fever for three weeks, with vomiting and confusion for four days. Neck stiffness on examination.",
		Course:  "CSF showed changes of tuberculosis. Started on anti-tubercular treatment and steroids. Consciousness improved steadily over two weeks.",
		Advice:  "Anti-tubercular medicines daily without a break for twelve months. Liver tests every month. Review in the medicine clinic after two weeks."},
	{Diagnosis: "Severe falciparum malaria with acute kidney injury",
		Tests:   []string{"Peripheral Smear for Malaria", "Complete Blood Count", "Renal Function Test", "Liver Function Test", "Serum Electrolytes", "Urine Routine Examination"},
		History: "High fever with chills for six days, then yellow eyes and reduced urine. Smear showed falciparum malaria.",
		Course:  "Treated with intravenous artesunate and careful fluids. Kidney function recovered slowly without dialysis. Smear negative on the fourth day.",
		Advice:  "Complete the oral antimalarial course. Mosquito nets. Repeat kidney function tests after one week."},
	{Diagnosis: "Diabetic foot infection with osteomyelitis", PreExisting: true,
		Tests:   []string{"HbA1c", "Pus Culture and Sensitivity", "X-Ray of the Injured Part", "Complete Blood Count", "Renal Function Test", "Blood Culture and Sensitivity"},
		History: "Known diabetic for fifteen years. A non-healing ulcer on the right foot for a month, now swollen with pus and fever.",
		Course:  "Treated with intravenous antibiotics by culture, daily dressings and insulin. The infection settled and the ulcer is granulating.",
		Advice:  "Daily dressing at the nearest clinic. Off-loading footwear. Strict sugar control with insulin. Review in the surgical clinic after one week."},
	{Diagnosis: "Amoebic liver abscess",
		Tests:   []string{"Ultrasound Abdomen", "Liver Function Test", "Complete Blood Count", "CT Abdomen with Contrast", "Coagulation Profile"},
		History: "Fever and pain under the right ribs for two weeks, with loss of appetite and weight.",
		Course:  "Ultrasound showed a 9 cm abscess, aspirated under guidance. Treated with intravenous metronidazole. Fever settled and the abscess shrank.",
		Advice:  "Complete the medicines. Boiled water only. Repeat ultrasound after four weeks."},
	{Diagnosis: "Infective endocarditis",
		Tests:   []string{"Blood Culture and Sensitivity", "2D Echocardiography", "Complete Blood Count", "Renal Function Test", "ECG", "Urine Routine Examination"},
		History: "Fever for three weeks with tiredness and breathlessness. A new heart murmur was heard at a clinic.",
		Course:  "Blood cultures grew streptococci and the echo showed a growth on the mitral valve. Treated with intravenous antibiotics; fever settled.",
		Advice:  "Continue intravenous antibiotics at the day care unit for two more weeks. Dental check before any procedure."},
	{Diagnosis: "Guillain-Barre syndrome",
		Tests:   []string{"CSF Analysis", "MRI Brain", "Serum Electrolytes", "Complete Blood Count", "Arterial Blood Gas"},
		History: "Weakness rising from both legs to the arms over five days after a bout of loose stools. Unable to walk.",
		Course:  "Treated with intravenous immunoglobulin over five days with monitoring of breathing. Power is improving with physiotherapy.",
		Advice:  "Daily physiotherapy. Walker for walking. Review in the neurology clinic after two weeks."},
	{Diagnosis: "Lung abscess",
		Tests:   []string{"CT Chest", "X-Ray Chest PA View", "Complete Blood Count", "Blood Culture and Sensitivity", "Liver Function Test"},
		History: "Cough with foul-smelling sputum and fever for three weeks, with weight loss.",
		Course:  "CT showed an abscess in the right lower lobe. Treated with intravenous antibiotics and postural drainage. Fever and sputum settled.",
		Advice:  "Complete six weeks of oral antibiotics. Breathing exercises. Repeat chest X-ray after four weeks."},
	{Diagnosis: "Acute on chronic pancreatitis",
		Tests:   []string{"Serum Amylase and Lipase", "CT Abdomen with Contrast", "Liver Function Test", "Serum Electrolytes", "HbA1c"},
		History: "Recurrent upper abdominal pain for two years; a severe attack with vomiting for three days.",
		Course:  "Managed with fluids, pain relief and gradual feeding. A collection near the pancreas was watched and shrank. Tolerating a normal diet.",
		Advice:  "Low-fat diet. No alcohol. Pancreatic enzyme capsules with meals. Review in the gastroenterology clinic after two weeks."},
}

// testPrices are the diagnostics' prices, in rupees.
var testPrices = map[string]int64{
	"Complete Blood Count": 450, "Liver Function Test": 900, "Renal Function Test": 850, "Serum Electrolytes": 700,
	"Urine Routine Examination": 250, "Blood Culture and Sensitivity": 1400, "X-Ray Chest PA View": 600,
	"Ultrasound Abdomen": 1800, "Dengue NS1 Antigen": 900, "Widal Test": 350, "ECG": 300, "2D Echocardiography": 2500,
	"Arterial Blood Gas": 1200, "CT Brain Plain": 3500, "MRI Brain": 7500, "Serum Amylase and Lipase": 1600,
	"HbA1c": 650, "Coagulation Profile": 950, "X-Ray Right Knee AP and Lateral": 700, "Troponin I": 1500,
	"CT Abdomen with Contrast": 6500, "Peripheral Smear for Malaria": 300, "CSF Analysis": 2200,
	"Pus Culture and Sensitivity": 1100, "X-Ray of the Injured Part": 650, "CT Chest": 5500,
	"Coronary Angiography": 12000, "Thyroid Profile": 750,
}

// drug is a pharmacy bill's item, its price in paise, and the most units
// one line issues.
type drug struct {
	Name  string
	Paise int64
	Max   int64
}

// drugs are what pharmacy bills issue: medicines and fluids only, none of
// them an Annexure A item.
var drugs = []drug{
	{"Inj. Ceftriaxone 1 g", 6850, 4}, {"Inj. Piperacillin-Tazobactam 4.5 g", 48500, 3},
	{"Inj. Meropenem 1 g", 145000, 3}, {"Inj. Pantoprazole 40 mg", 5200, 4},
	{"Inj. Ondansetron 4 mg", 1850, 6}, {"Tab. Paracetamol 650 mg", 210, 20},
	{"IV Fluid Ringer Lactate 500 ml", 4200, 8}, {"IV Fluid Normal Saline 500 ml", 3500, 8},
	{"IV Fluid Dextrose Saline 500 ml", 3900, 6}, {"Inj. Human Insulin 40 IU/ml", 16500, 2},
	{"Inj. Enoxaparin 40 mg", 38500, 3}, {"Tab. Metformin 500 mg", 180, 30},
	{"Inj. Amikacin 500 mg", 6200, 4}, {"Inj. Vancomycin 1 g", 59000, 3},
	{"Syp. Lactulose 200 ml", 21500, 2}, {"Inj. Furosemide 20 mg", 1100, 6},
	{"Tab. Rifampicin 450 mg", 650, 15}, {"Tab. Isoniazid 300 mg", 120, 15},
	{"Inj. Artesunate 60 mg", 35000, 4}, {"Inj. Dexamethasone 8 mg", 1600, 4},
	{"Inj. Metronidazole 500 mg", 2900, 6}, {"Tab. Amlodipine 5 mg", 350, 15},
	{"Tab. Atorvastatin 40 mg", 1450, 15}, {"Tab. Clopidogrel 75 mg", 950, 15},
	{"Tab. Aspirin 75 mg", 150, 15}, {"Inj. Heparin 5000 IU", 9800, 4},
	{"Inj. Tramadol 50 mg", 1250, 6}, {"Inj. Diclofenac 75 mg", 900, 6},
	{"Tab. Pantoprazole 40 mg", 650, 15}, {"Syp. Potassium Chloride 200 ml", 9500, 2},
	{"Inj. Levetiracetam 500 mg", 24500, 4}, {"Inj. Human Albumin 20% 100 ml", 520000, 2},
	{"Inj. Hydrocortisone 100 mg", 4800, 4}, {"Salbutamol Respules 2.5 mg", 850, 12},
	{"Tab. Cefixime 200 mg", 1100, 14}, {"Inj. Cefoperazone-Sulbactam 1.5 g", 36500, 3},
}

// eyeDrugs are what a cataract packet's pharmacy issues.
var eyeDrugs = []drug{
	{"Moxifloxacin Eye Drops 5 ml", 14500, 2}, {"Prednisolone Acetate Eye Drops 5 ml", 9800, 2},
	{"Nepafenac Eye Drops 5 ml", 21500, 1}, {"Carboxymethylcellulose Eye Drops 10 ml", 16500, 2},
}

// listedChoices are the Annexure A items a seeded bill may list, as
// other-hospital-charges, with their prices in rupees.
var listedChoices = []struct {
	Key    string
	Rupees int64
}{
	{"list-ii/27", 750}, {"list-ii/30", 500}, {"list-iv/1", 300}, {"list-i/11", 400}, {"list-i/14", 150},
}

// The invented places and people.
var (
	seededHospitals = []datagen.Hospital{
		{Name: "Nayakhet Multispeciality Hospital", Address: "21 Mandi Road", City: "Nayakhet"},
		{Name: "Chandmora Medical Centre", Address: "Plot 4, Ring Road", City: "Chandmora"},
		{Name: "Pallavigram City Hospital", Address: "8 Court Lane", City: "Pallavigram"},
		{Name: "Sitarkund Care Hospital", Address: "17 Lake Road, Sector 2", City: "Sitarkund"},
		{Name: "Amberpur General Hospital", Address: "14 Lakeview Road, Ward 3", City: "Amberpur"},
		{Name: "Ravikund Multispeciality Hospital", Address: "Plot 7, Station Road", City: "Ravikund"},
		{Name: "Tarangpur City Hospital", Address: "22 Canal Street", City: "Tarangpur"},
		{Name: "Kesarvan Heart and General Hospital", Address: "3 Fort Road", City: "Kesarvan"},
	}
	seededEyeHospitals = []datagen.Hospital{
		{Name: "Drishtipur Eye Hospital", Address: "9 Market Road", City: "Drishtipur"},
		{Name: "Nayanpur Eye Care Centre", Address: "12 Temple Street", City: "Nayanpur"},
		{Name: "Chandmora Eye Institute", Address: "Plot 9, Ring Road", City: "Chandmora"},
	}
	vendors = []datagen.Vendor{
		{Name: "Kesarvan Surgical Supplies", Address: "5 Mill Road, Kesarvan"},
		{Name: "Nayakhet Ortho Devices", Address: "40 Industrial Area, Nayakhet"},
		{Name: "Chandmora Cardio Supplies", Address: "11 Ring Road, Chandmora"},
	}
	lensVendor    = datagen.Vendor{Name: "Drishti Lens Distributors", Address: "2 Market Road, Drishtipur"}
	firstNamesF   = []string{"Asha", "Bhavana", "Chitra", "Deepika", "Farzana", "Gauri", "Hema", "Ishita", "Jyoti", "Kiran", "Lata", "Madhavi"}
	firstNamesM   = []string{"Anil", "Bharat", "Chetan", "Dinesh", "Farhan", "Gopal", "Hemant", "Irfan", "Jatin", "Kunal", "Mohan", "Naveen"}
	surnames      = []string{"Acharya", "Bhosale", "Chatterjee", "Dsouza", "Gill", "Hegde", "Iyengar", "Jadhav", "Kamath", "Lobo", "Mistry", "Nair"}
	physicians    = []string{"Dr. Meghna Iyer", "Dr. Sameer Joshi", "Dr. Pooja Venkatesh", "Dr. Arvind Saxena", "Dr. Neha Kapadia", "Dr. Tanvir Ahmed", "Dr. Ananya Rao", "Dr. Kavin Raman"}
	surgeons      = []string{"Dr. Rohit Bhalla", "Dr. Leela Menon", "Dr. Farhan Siddiqui", "Dr. Vivek Rathore", "Dr. Sarita Gokhale", "Dr. Nikhil Bansal"}
	anaesthetists = []string{"Dr. Sunita Kher", "Dr. Ravi Shenoy", "Dr. Anjali Dutta", "Dr. Manoj Pillai"}
	roomTypes     = []string{"Room Rent - Twin Sharing", "Room Charges - Private Room", "Ward Charges - General Ward", "Room Rent - Deluxe Single", "Room Rent - Semi Private"}
	paymentModes  = []string{"UPI", "Card", "Cash", "NEFT", "Cheque"}
)
