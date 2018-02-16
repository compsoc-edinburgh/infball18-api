package charge

import (
	"net/http"
	"strconv"

	"github.com/badoux/checkmail"
	"github.com/compsoc-edinburgh/infball-api/pkg/api/base"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	stripe "github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/currency"
)

func isOneOf(one string, other ...string) bool {
	for _, v := range other {
		if v == one {
			return true
		}
	}
	return false
}

func (i *Impl) MakeCharge(c *gin.Context) {
	var result struct {
		Token string

		FullName    string
		UUN         string
		Email       string
		Over18      bool
		Starter     string
		Main        string
		Dessert     string
		SpecialReqs string
	}

	if err := c.BindJSON(&result); err != nil {
		base.BadRequest(c, err.Error())
		return
	}

	if result.Token == "" {
		base.BadRequest(c, "Stripe token missing.")
		return
	}

	if !result.Over18 {
		base.BadRequest(c, "You must be atleast 18 years of age to attend.")
		return
	}

	if result.FullName == "" {
		base.BadRequest(c, "Full name missing.")
		return
	}

	if checkmail.ValidateFormat(result.Email) != nil {
		base.BadRequest(c, "Invalid email format provided. Please email infball@comp-soc.com if this is a mistake.")
		return
	}

	if !base.CheckUUN(c, result.UUN) {
		return
	}

	if !isOneOf(result.Starter, "soup", "salmon", "pork") || !isOneOf(result.Main, "beef", "salmon", "chicken", "mushrooms") || !isOneOf(result.Dessert, "brownie", "toffee") {
		base.BadRequest(c, "Invalid food selection.")
		return
	}

	if len(result.SpecialReqs) > 500 {
		base.BadRequest(c, "Sorry, your request is limited to 500 characters. Please email infball@comp-soc.com for assistance.")
		return
	}

	sku, err := i.Stripe.Skus.Get(i.Config.Stripe.SKU, nil)
	if err != nil {
		msg := err.Error()
		if stripeErr, ok := err.(*stripe.Error); ok {
			msg = stripeErr.Msg
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": msg,
		})
		return
	}

	if sku.Inventory.Quantity == 0 {
		c.JSON(http.StatusGone, gin.H{
			"status":  "error",
			"message": "Sorry! We have run out of tickets... for now.",
		})
		return
	}

	authToken := uuid.New().String()

	order, err := i.Stripe.Orders.New(&stripe.OrderParams{
		Currency: currency.GBP,
		Items: []*stripe.OrderItemParams{
			&stripe.OrderItemParams{
				Type:   "sku",
				Parent: i.Config.Stripe.SKU,
			},
		},
		Params: stripe.Params{
			Meta: map[string]string{
				"uun":              result.UUN,
				"purchaser_email":  result.Email,
				"purchaser_name":   result.FullName,
				"owner_email":      result.Email,
				"owner_name":       result.FullName,
				"over18":           strconv.FormatBool(result.Over18),
				"meal_starter":     result.Starter,
				"meal_main":        result.Main,
				"meal_dessert":     result.Dessert,
				"special_requests": result.SpecialReqs,
				"auth_token":       authToken,
			},
		},
		Email: result.Email,
	})

	if err != nil {
		msg := err.Error()
		if stripeErr, ok := err.(*stripe.Error); ok {
			msg = stripeErr.Msg
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": msg,
		})
		return
	}

	// Charge the user's card:
	params := &stripe.OrderPayParams{}
	params.SetSource(result.Token)

	// Actually pay the user
	o, err := i.Stripe.Orders.Pay(order.ID, params)
	if err != nil {
		msg := err.Error()
		if stripeErr, ok := err.(*stripe.Error); ok {
			msg = stripeErr.Msg
		}

		base.BadRequest(c, msg)
		return
	}

	go i.Stripe.Charges.Update(o.Charge.ID, &stripe.ChargeParams{
		Desc: "Informatics Ball Ticket",
	})

	if !base.SendTicketEmail(c, i.Mailgun, result.FullName, result.Email, o.ID, authToken) {
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   o.ID,
	})
}
