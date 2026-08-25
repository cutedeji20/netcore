package customers

import "testing"

func TestCreateCanonicalisesEmailAndRejectsInvalidProfileData(t *testing.T) {
	input := WriteInput{FirstName: " Chika ", LastName: " Nwosu ", Email: " CHIKA@Example.test ", Phone: " +2348031114280 "}
	if err := input.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if input.FirstName != "Chika" || input.LastName != "Nwosu" || input.Email != "chika@example.test" || input.Phone != "+2348031114280" {
		t.Fatalf("normalized input = %+v", input)
	}

	for _, input := range []WriteInput{
		{FirstName: "", LastName: "Nwosu", Email: "chika@example.test"},
		{FirstName: "Chika", LastName: "", Email: "chika@example.test"},
		{FirstName: "Chika", LastName: "Nwosu", Email: "not-an-email"},
		{FirstName: "Chika", LastName: "Nwosu", Email: "chika@example.test", Phone: "not a phone"},
	} {
		if err := input.NormalizeAndValidate(); err == nil {
			t.Fatalf("invalid input accepted: %+v", input)
		}
	}
}
